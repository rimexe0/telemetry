package signin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rimexe0/telemetry/internal/clerkidentity"
)

// ErrUnavailable means a change cannot be made on this server as it is set up,
// for example without TELEMETRY_PUBLIC_URL.
var ErrUnavailable = errors.New("sign-in change unavailable")

// ErrTokenRejected is a Clerk session token that did not verify.
var ErrTokenRejected = errors.New("Clerk session token rejected")

// ManagerConfig is fixed for the life of the process.
type ManagerConfig struct {
	// PublicURL is the panel's HTTPS origin. Clerk puts the browser's origin
	// in each session token's azp claim, so tokens are accepted only when it
	// equals this. Without it Clerk cannot be linked or used.
	PublicURL string
	// HTTPClient fetches Clerk signing keys. Tests replace it.
	HTTPClient *http.Client
	// Now is the clock for token checks. Tests replace it.
	Now func() time.Time
}

// Manager holds the current sign-in settings and the Clerk verifier built from
// them. Both are swapped together atomically, so a saved change applies to
// the next request without a restart.
type Manager struct {
	store     Store
	publicURL string
	client    *http.Client
	now       func() time.Time

	mu    sync.Mutex // serializes rebuilds of current
	state atomic.Pointer[state]
}

type state struct {
	settings Settings
	owner    *clerkidentity.Verifier
}

// NewManager starts with Defaults; call Reload to read the stored settings.
func NewManager(store Store, config ManagerConfig) (*Manager, error) {
	if store == nil {
		return nil, errors.New("sign-in store is required")
	}
	publicURL := strings.TrimRight(strings.TrimSpace(config.PublicURL), "/")
	if publicURL != "" {
		parsed, err := url.Parse(publicURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			// Clerk can still be shown but never linked; the panel token works.
			log.Printf("sign-in: TELEMETRY_PUBLIC_URL %q is not an HTTPS origin, so Clerk cannot be linked", config.PublicURL)
			publicURL = ""
		} else {
			publicURL = "https://" + strings.ToLower(parsed.Host)
		}
	}
	m := &Manager{store: store, publicURL: publicURL, client: config.HTTPClient, now: config.Now}
	m.state.Store(&state{settings: Defaults()})
	return m, nil
}

// PublicURL is the origin Clerk tokens must name, or empty when unset.
func (m *Manager) PublicURL() string { return m.publicURL }

// Settings returns the settings now in force.
func (m *Manager) Settings() Settings { return m.state.Load().settings }

// PanelTokenEnabled reports whether the panel token may sign the owner in.
func (m *Manager) PanelTokenEnabled() bool { return m.state.Load().settings.PanelTokenEnabled }

// PublishableKey is the Clerk key the sign-in screen loads, only while Clerk
// sign-in is on.
func (m *Manager) PublishableKey() string {
	current := m.state.Load()
	if !current.settings.ClerkEnabled || current.owner == nil {
		return ""
	}
	return current.settings.Clerk.PublishableKey
}

// Issuer is the Clerk Frontend API origin the panel may load scripts from:
// set while a key is stored, so the owner can sign in to link it.
func (m *Manager) Issuer() string { return m.state.Load().settings.Clerk.Issuer }

// ClerkOwner reports whether the request carries a Clerk session token for
// the linked owner while Clerk sign-in is on.
func (m *Manager) ClerkOwner(request *http.Request) bool {
	current := m.state.Load()
	if !current.settings.ClerkEnabled || current.owner == nil {
		return false
	}
	_, err := current.owner.VerifyAuthorization(request.Context(), request.Header.Get("Authorization"))
	return err == nil
}

// Reload reads the stored settings and swaps them in.
func (m *Manager) Reload(ctx context.Context) error {
	settings, err := m.store.Load(ctx)
	if err != nil {
		return err
	}
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("stored sign-in settings are invalid: %w", err)
	}
	m.swap(settings)
	return nil
}

// Watch reloads the settings every interval, so a change made by another
// process, such as telemetryd -reset-sign-in, reaches this one.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Reload(ctx); err != nil && ctx.Err() == nil {
				log.Printf("sign-in: reload settings: %v", err)
			}
		}
	}
}

func (m *Manager) swap(settings Settings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.state.Load()
	next := &state{settings: settings}
	if settings.Linked() && m.publicURL != "" {
		// Keep the verifier, and its cached signing keys, when nothing it
		// checks has changed.
		if previous.owner != nil && previous.settings.Clerk == settings.Clerk {
			next.owner = previous.owner
		} else {
			verifier, err := m.verifier(settings.Clerk.Issuer, settings.Clerk.OwnerUserID)
			if err != nil {
				log.Printf("sign-in: Clerk verifier: %v", err)
			} else {
				next.owner = verifier
			}
		}
	}
	m.state.Store(next)
}

func (m *Manager) verifier(issuer, owner string) (*clerkidentity.Verifier, error) {
	return clerkidentity.New(clerkidentity.Config{
		Issuer:            issuer,
		JWKSURL:           issuer + "/.well-known/jwks.json",
		OwnerUserID:       owner,
		AnySubject:        owner == "",
		AuthorizedParties: []string{m.publicURL},
		HTTPClient:        m.client,
		Now:               m.now,
	})
}

func (m *Manager) update(ctx context.Context, change string, mutate func(Settings) (Settings, error)) (Settings, error) {
	settings, err := m.store.Update(ctx, change, mutate)
	if err != nil {
		return Settings{}, err
	}
	m.swap(settings)
	log.Printf("sign-in: %s", change)
	return settings, nil
}

// SetMethods turns the panel token and Clerk sign-in on or off.
func (m *Manager) SetMethods(ctx context.Context, panelToken, clerk bool) (Settings, error) {
	change := fmt.Sprintf("panel token %s, Clerk %s", onOff(panelToken), onOff(clerk))
	return m.update(ctx, change, func(current Settings) (Settings, error) {
		current.PanelTokenEnabled = panelToken
		current.ClerkEnabled = clerk
		return current, nil
	})
}

// SetClerkKey stores a publishable key to link. A new key needs a new link,
// so it clears any linked owner and turns Clerk sign-in off.
func (m *Manager) SetClerkKey(ctx context.Context, key string) (Settings, error) {
	if m.publicURL == "" {
		return Settings{}, fmt.Errorf("%w: set TELEMETRY_PUBLIC_URL to this panel's HTTPS address before adding Clerk", ErrUnavailable)
	}
	key = strings.TrimSpace(key)
	issuer, err := IssuerFromPublishableKey(key)
	if err != nil {
		return Settings{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	return m.update(ctx, "Clerk publishable key set for "+issuer, func(current Settings) (Settings, error) {
		if current.Clerk.PublishableKey == key {
			return current, nil
		}
		current.Clerk = Clerk{PublishableKey: key, Issuer: issuer}
		current.ClerkEnabled = false
		return current, nil
	})
}

// Link verifies a fresh Clerk session token from the stored key's instance and
// makes its user the owner. Clerk sign-in stays off until turned on.
func (m *Manager) Link(ctx context.Context, token string) (Settings, error) {
	if m.publicURL == "" {
		return Settings{}, fmt.Errorf("%w: set TELEMETRY_PUBLIC_URL to this panel's HTTPS address before linking Clerk", ErrUnavailable)
	}
	pending := m.Settings().Clerk
	if pending.PublishableKey == "" {
		return Settings{}, fmt.Errorf("%w: add a Clerk publishable key first", ErrRejected)
	}
	verifier, err := m.verifier(pending.Issuer, "")
	if err != nil {
		return Settings{}, err
	}
	claims, err := verifier.Verify(ctx, strings.TrimSpace(token))
	if err != nil {
		if errors.Is(err, clerkidentity.ErrJWKSUnavailable) {
			return Settings{}, fmt.Errorf("%w: could not read Clerk's signing keys from %s", ErrTokenRejected, pending.Issuer)
		}
		return Settings{}, fmt.Errorf("%w: it must come from a signed-in session of this Clerk application, opened at %s", ErrTokenRejected, m.publicURL)
	}
	return m.update(ctx, "Clerk account "+claims.Subject+" linked", func(current Settings) (Settings, error) {
		if current.Clerk.PublishableKey != pending.PublishableKey {
			return Settings{}, fmt.Errorf("%w: the Clerk key changed while linking; try again", ErrRejected)
		}
		if current.Clerk.OwnerUserID != claims.Subject {
			// A different account must be turned on again deliberately.
			current.ClerkEnabled = false
		}
		current.Clerk.OwnerUserID = claims.Subject
		return current, nil
	})
}

// Unlink forgets the Clerk application and turns Clerk sign-in off. It is
// refused while the panel token is off, which would leave no way in.
func (m *Manager) Unlink(ctx context.Context) (Settings, error) {
	return m.update(ctx, "Clerk removed", func(current Settings) (Settings, error) {
		if !current.PanelTokenEnabled {
			return Settings{}, fmt.Errorf("%w: turn the panel token on before removing Clerk", ErrRejected)
		}
		current.Clerk = Clerk{}
		current.ClerkEnabled = false
		return current, nil
	})
}

// Reset turns the panel token back on so a locked-out owner can sign in. It
// keeps Clerk as it is when Clerk is valid.
func Reset(ctx context.Context, store Store) (Settings, error) {
	return store.Update(ctx, "panel token turned back on with telemetryd -reset-sign-in", func(current Settings) (Settings, error) {
		current.PanelTokenEnabled = true
		if current.ClerkEnabled && !current.Linked() {
			current.ClerkEnabled = false
		}
		if current.Validate() != nil {
			current.Clerk = Clerk{}
			current.ClerkEnabled = false
		}
		return current, nil
	})
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}
