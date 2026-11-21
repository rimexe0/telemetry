package signin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testPublicURL = "https://panel.example.ts.net:8443"
	testHost      = "fond-owl-12.clerk.accounts.dev"
	testIssuer    = "https://" + testHost
)

var testKey = "pk_test_" + base64.StdEncoding.EncodeToString([]byte(testHost+"$"))

func TestValidateRules(t *testing.T) {
	linked := Clerk{PublishableKey: testKey, Issuer: testIssuer, OwnerUserID: "user_1"}
	for _, tc := range []struct {
		name     string
		settings Settings
		ok       bool
	}{
		{"defaults", Defaults(), true},
		{"nothing on", Settings{}, false},
		{"Clerk on unlinked", Settings{PanelTokenEnabled: true, ClerkEnabled: true, Clerk: Clerk{PublishableKey: testKey, Issuer: testIssuer}}, false},
		{"Clerk on without key", Settings{PanelTokenEnabled: true, ClerkEnabled: true}, false},
		{"both on", Settings{PanelTokenEnabled: true, ClerkEnabled: true, Clerk: linked}, true},
		{"Clerk only", Settings{ClerkEnabled: true, Clerk: linked}, true},
		{"panel off with Clerk linked but off", Settings{Clerk: linked}, false},
		{"pending key", Settings{PanelTokenEnabled: true, Clerk: Clerk{PublishableKey: testKey, Issuer: testIssuer}}, true},
		{"issuer mismatch", Settings{PanelTokenEnabled: true, Clerk: Clerk{PublishableKey: testKey, Issuer: "https://attacker.example"}}, false},
		{"owner without key", Settings{PanelTokenEnabled: true, Clerk: Clerk{OwnerUserID: "user_1"}}, false},
	} {
		err := tc.settings.Validate()
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrRejected)) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestIssuerFromPublishableKey(t *testing.T) {
	encode := func(prefix, host string) string { return prefix + base64.StdEncoding.EncodeToString([]byte(host)) }
	if issuer, err := IssuerFromPublishableKey(" " + testKey + " "); err != nil || issuer != testIssuer {
		t.Fatalf("dev key: %q %v", issuer, err)
	}
	if issuer, err := IssuerFromPublishableKey(encode("pk_live_", "clerk.example.com$")); err != nil || issuer != "https://clerk.example.com" {
		t.Fatalf("live key: %q %v", issuer, err)
	}
	for _, bad := range []string{
		"", "sk_test_abc", "pk_test_%%%", encode("pk_test_", "clerk.example.com"),
		encode("pk_test_", "clerk.example.com:8443$"), encode("pk_test_", "127.0.0.1$"),
		encode("pk_test_", "localhost$"), encode("pk_test_", "clerk.example.com/path$"),
		encode("pk_test_", "user@clerk.example.com$"), encode("pk_test_", "clerk example.com$"),
		encode("pk_test_", "clerk.example.com; script-src *$"),
	} {
		if issuer, err := IssuerFromPublishableKey(bad); err == nil {
			t.Errorf("accepted %q as %q", bad, issuer)
		}
	}
}

// memoryStore is the Store contract without a database.
type memoryStore struct {
	mu       sync.Mutex
	settings *Settings
	changes  []Change
}

func (s *memoryStore) Load(context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings == nil {
		return Defaults(), nil
	}
	return *s.settings, nil
}

func (s *memoryStore) Update(_ context.Context, change string, mutate func(Settings) (Settings, error)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := Defaults()
	if s.settings != nil {
		previous = *s.settings
	}
	next, err := mutate(previous)
	if err != nil {
		return Settings{}, err
	}
	if err := next.Validate(); err != nil {
		return Settings{}, err
	}
	if next != previous {
		s.changes = append([]Change{{ChangedAt: time.Now(), Change: change, Previous: previous, Value: next}}, s.changes...)
	}
	s.settings = &next
	return next, nil
}

func (s *memoryStore) Changes(_ context.Context, limit int) ([]Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Change(nil), s.changes[:min(limit, len(s.changes))]...), nil
}

// fakeClerk signs session tokens and serves its JWKS to the manager's client.
type fakeClerk struct {
	key  *rsa.PrivateKey
	now  time.Time
	jwks string
}

func newFakeClerk(t *testing.T) *fakeClerk {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
	}}})
	return &fakeClerk{key: key, now: time.Unix(1_800_000_000, 0), jwks: string(jwks)}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (c *fakeClerk) client() *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != testIssuer+"/.well-known/jwks.json" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(c.jwks)), Header: http.Header{"Content-Type": {"application/json"}}, Request: r}, nil
	})}
}

func (c *fakeClerk) token(t *testing.T, sub, azp string) string {
	t.Helper()
	encode := func(value any) string {
		data, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(data)
	}
	input := encode(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." + encode(map[string]any{
		"sub": sub, "iss": testIssuer, "azp": azp, "sts": "active",
		"iat": c.now.Unix() - 5, "nbf": c.now.Unix() - 5, "exp": c.now.Unix() + 60,
	})
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.key, 5, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func newTestManager(t *testing.T, clerk *fakeClerk) (*Manager, *memoryStore) {
	t.Helper()
	store := &memoryStore{}
	m, err := NewManager(store, ManagerConfig{PublicURL: testPublicURL + "/", HTTPClient: clerk.client(), Now: func() time.Time { return clerk.now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	return m, store
}

func bearer(token string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// Saving a change swaps the verifier the next request uses, with no restart.
func TestManagerSwapsTheVerifier(t *testing.T) {
	clerk := newFakeClerk(t)
	m, store := newTestManager(t, clerk)
	ctx := t.Context()
	owner := clerk.token(t, "user_owner", testPublicURL)
	if m.ClerkOwner(bearer(owner)) || !m.PanelTokenEnabled() || m.Issuer() != "" {
		t.Fatal("defaults are not the panel token alone")
	}
	if _, err := m.SetMethods(ctx, true, true); !errors.Is(err, ErrRejected) {
		t.Fatalf("Clerk turned on without a key: %v", err)
	}
	if _, err := m.SetClerkKey(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	if m.Issuer() != testIssuer || m.PublishableKey() != "" {
		t.Fatalf("pending key: issuer %q key %q", m.Issuer(), m.PublishableKey())
	}
	if _, err := m.SetMethods(ctx, true, true); !errors.Is(err, ErrRejected) {
		t.Fatalf("Clerk turned on before linking: %v", err)
	}
	if _, err := m.Link(ctx, clerk.token(t, "user_owner", "https://attacker.example")); !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("linked a token for another origin: %v", err)
	}
	if _, err := m.Link(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if m.ClerkOwner(bearer(owner)) {
		t.Fatal("linking turned Clerk sign-in on by itself")
	}
	if _, err := m.SetMethods(ctx, false, true); err != nil {
		t.Fatal(err)
	}
	if !m.ClerkOwner(bearer(owner)) || m.PanelTokenEnabled() || m.PublishableKey() != testKey {
		t.Fatal("Clerk-only settings did not take effect")
	}
	if m.ClerkOwner(bearer(clerk.token(t, "user_other", testPublicURL))) || m.ClerkOwner(bearer(clerk.token(t, "user_owner", "https://attacker.example"))) {
		t.Fatal("accepted another account or origin")
	}
	if _, err := m.Unlink(ctx); !errors.Is(err, ErrRejected) {
		t.Fatalf("removed Clerk with the panel token off: %v", err)
	}
	if _, err := m.SetClerkKey(ctx, "pk_test_"+base64.StdEncoding.EncodeToString([]byte("other.clerk.accounts.dev$"))); !errors.Is(err, ErrRejected) {
		t.Fatalf("a new key left no way in: %v", err)
	}
	if _, err := m.SetMethods(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Unlink(ctx); err != nil {
		t.Fatal(err)
	}
	if m.ClerkOwner(bearer(owner)) || m.Issuer() != "" {
		t.Fatal("unlinked Clerk still signs in")
	}
	changes, _ := store.Changes(ctx, 10)
	if len(changes) != 5 || changes[0].Change != "Clerk removed" || !changes[0].Previous.Linked() {
		t.Fatalf("history %+v", changes)
	}
}

// A change made elsewhere, such as -reset-sign-in, reaches a running server.
func TestReloadAndReset(t *testing.T) {
	clerk := newFakeClerk(t)
	m, store := newTestManager(t, clerk)
	ctx := t.Context()
	owner := clerk.token(t, "user_owner", testPublicURL)
	for _, step := range []func() error{
		func() error { _, err := m.SetClerkKey(ctx, testKey); return err },
		func() error { _, err := m.Link(ctx, owner); return err },
		func() error { _, err := m.SetMethods(ctx, false, true); return err },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := Reset(ctx, store)
	if err != nil || !settings.PanelTokenEnabled || !settings.ClerkEnabled {
		t.Fatalf("reset: %+v %v", settings, err)
	}
	if m.PanelTokenEnabled() {
		t.Fatal("reload happened before it was asked for")
	}
	if err := m.Reload(ctx); err != nil || !m.PanelTokenEnabled() || !m.ClerkOwner(bearer(owner)) {
		t.Fatalf("reload: %v", err)
	}
}

func TestLinkNeedsPublicURL(t *testing.T) {
	m, err := NewManager(&memoryStore{}, ManagerConfig{PublicURL: "http://panel.example"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetClerkKey(t.Context(), testKey); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("accepted a key without an HTTPS public URL: %v", err)
	}
	if _, err := m.Link(t.Context(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("linked without a public URL: %v", err)
	}
}
