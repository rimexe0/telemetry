// Package signin keeps how the owner signs in to the panel: the panel token,
// their own Clerk application, or both. The settings live in the database so a
// person who hosts Telemetry can change them from the panel without editing
// the environment or restarting. A Clerk configuration in the environment
// always wins and leaves these settings unused.
package signin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrRejected is a change the rules do not allow, such as turning every
// sign-in method off. Its message says what to do instead.
var ErrRejected = errors.New("sign-in change rejected")

// Settings are the stored sign-in choices. The publishable key, its issuer and
// the owner's Clerk user ID are not secrets; nothing secret is stored here.
type Settings struct {
	PanelTokenEnabled bool  `json:"panel_token_enabled"`
	ClerkEnabled      bool  `json:"clerk_enabled"`
	Clerk             Clerk `json:"clerk"`
}

// Clerk is the person's own Clerk application. OwnerUserID is empty until a
// signed-in Clerk session has been verified and linked.
type Clerk struct {
	PublishableKey string `json:"publishable_key,omitempty"`
	Issuer         string `json:"issuer,omitempty"`
	OwnerUserID    string `json:"owner_user_id,omitempty"`
}

// Defaults is the panel token alone, which is how a new server starts.
func Defaults() Settings { return Settings{PanelTokenEnabled: true} }

// Linked reports whether a Clerk owner account has been verified.
func (s Settings) Linked() bool {
	return s.Clerk.PublishableKey != "" && s.Clerk.Issuer != "" && s.Clerk.OwnerUserID != ""
}

// Validate enforces the rules: at least one method stays on, Clerk is on only
// with a linked owner, and the panel token goes off only while Clerk is on.
func (s Settings) Validate() error {
	if s.Clerk.PublishableKey == "" {
		if s.Clerk.Issuer != "" || s.Clerk.OwnerUserID != "" {
			return fmt.Errorf("%w: Clerk details need a publishable key", ErrRejected)
		}
	} else {
		issuer, err := IssuerFromPublishableKey(s.Clerk.PublishableKey)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
		if s.Clerk.Issuer != issuer {
			return fmt.Errorf("%w: Clerk issuer does not match the publishable key", ErrRejected)
		}
	}
	if s.ClerkEnabled && !s.Linked() {
		return fmt.Errorf("%w: link a Clerk account before turning Clerk sign-in on", ErrRejected)
	}
	if !s.PanelTokenEnabled && !s.ClerkEnabled {
		return fmt.Errorf("%w: keep at least one way to sign in; turn Clerk sign-in on before turning the panel token off", ErrRejected)
	}
	return nil
}

// FrontendHost decodes the Clerk Frontend API host from a publishable key:
// pk_test_ or pk_live_ followed by base64 of the host and a trailing "$".
func FrontendHost(key string) (string, error) {
	encoded := strings.TrimPrefix(strings.TrimPrefix(key, "pk_test_"), "pk_live_")
	if encoded == key {
		return "", errors.New("invalid Clerk publishable key prefix")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil || !strings.HasSuffix(string(raw), "$") {
		return "", errors.New("invalid Clerk publishable key")
	}
	return strings.TrimSuffix(string(raw), "$"), nil
}

var frontendHostPattern = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// IssuerFromPublishableKey returns the HTTPS issuer for a pasted key. It is
// stricter than FrontendHost: the server fetches signing keys from this host,
// so it must be a plain DNS name with no port, path or address literal.
func IssuerFromPublishableKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) > 512 {
		return "", errors.New("invalid Clerk publishable key")
	}
	host, err := FrontendHost(key)
	if err != nil {
		return "", err
	}
	if len(host) > 253 || !frontendHostPattern.MatchString(host) || isNumericHost(host) {
		return "", errors.New("Clerk publishable key names an invalid Frontend API host")
	}
	return "https://" + strings.ToLower(host), nil
}

func isNumericHost(host string) bool {
	last := host[strings.LastIndex(host, ".")+1:]
	return strings.Trim(last, "0123456789") == ""
}
