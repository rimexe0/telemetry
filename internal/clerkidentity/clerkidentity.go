// Package clerkidentity verifies Clerk session JWTs for the single Telemetry
// owner. It deliberately owns only token verification; device enrollment and
// data authorization remain responsibilities of the HTTP API.
package clerkidentity

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultJWKSCacheTTL     = 10 * time.Minute
	defaultJWKSFetchTimeout = 5 * time.Second
	defaultUnknownKeyRetry  = 10 * time.Second
	maxJWKSBodyBytes        = 1 << 20
	maxJWKSKeys             = 64
	maxTokenBytes           = 64 << 10
)

var (
	// ErrInvalidToken is returned for a malformed, incorrectly signed, or
	// unauthorized Clerk token. Callers should normally map it to HTTP 401.
	ErrInvalidToken = errors.New("invalid clerk token")
	// ErrJWKSUnavailable indicates that the configured key endpoint could not
	// provide a usable key set. It is kept separate for operational logging;
	// callers should still treat it as an authentication failure.
	ErrJWKSUnavailable = errors.New("clerk jwks unavailable")
)

// Config describes the one Telemetry owner and the Clerk instance that may
// issue tokens for it. Values are intentionally exact: issuer and authorized
// party strings are compared as configured, without URL normalization.
type Config struct {
	JWKSURL           string
	Issuer            string
	AuthorizedParties []string
	OwnerUserID       string
	Audience          string
	HTTPClient        *http.Client
	JWKSCacheTTL      time.Duration
	JWKSFetchTimeout  time.Duration
	UnknownKeyRetry   time.Duration
	ClockSkew         time.Duration
	Now               func() time.Time
}

// Claims contains the verified identity and the standard time claims used by
// the verifier. Subject is guaranteed to equal Config.OwnerUserID.
type Claims struct {
	Subject         string
	Issuer          string
	AuthorizedParty string
	Audience        []string
	KeyID           string
	ExpiresAt       time.Time
	NotBefore       *time.Time
	IssuedAt        *time.Time
}

// Verifier verifies Clerk RS256 JWTs and caches a bounded JWKS snapshot.
type Verifier struct {
	config Config
	client *http.Client

	mu                 sync.Mutex
	keys               map[string]*rsa.PublicKey
	fetchedAt          time.Time
	lastUnknownRefresh time.Time
	refreshing         bool
	refreshDone        chan struct{}
}

// New validates configuration and returns a verifier. The JWKS endpoint is
// fetched lazily on the first Verify call, so constructing a verifier does not
// require network access.
func New(config Config) (*Verifier, error) {
	if strings.TrimSpace(config.JWKSURL) == "" {
		return nil, errors.New("jwks url is required")
	}
	parsed, err := url.Parse(config.JWKSURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("jwks url must be an http or https URL")
	}
	if strings.TrimSpace(config.Issuer) == "" {
		return nil, errors.New("issuer is required")
	}
	if strings.TrimSpace(config.OwnerUserID) == "" {
		return nil, errors.New("owner user id is required")
	}
	if len(config.AuthorizedParties) == 0 {
		return nil, errors.New("at least one authorized party is required")
	}
	for _, party := range config.AuthorizedParties {
		if strings.TrimSpace(party) == "" {
			return nil, errors.New("authorized parties cannot be empty")
		}
	}
	if config.JWKSCacheTTL <= 0 {
		config.JWKSCacheTTL = defaultJWKSCacheTTL
	}
	if config.JWKSFetchTimeout <= 0 {
		config.JWKSFetchTimeout = defaultJWKSFetchTimeout
	}
	if config.UnknownKeyRetry <= 0 {
		config.UnknownKeyRetry = defaultUnknownKeyRetry
	}
	if config.ClockSkew < 0 {
		return nil, errors.New("clock skew cannot be negative")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: config.JWKSFetchTimeout}
	}
	return &Verifier{config: config, client: client}, nil
}

// VerifyAuthorization extracts a Bearer token from an Authorization header
// and verifies it. Cookie extraction is intentionally left to the HTTP layer so
// this package stays usable by collectors and non-browser clients alike.
func (v *Verifier) VerifyAuthorization(ctx context.Context, authorization string) (Claims, error) {
	if len(authorization) > maxTokenBytes+len("Bearer ") {
		return Claims{}, ErrInvalidToken
	}
	if !strings.HasPrefix(authorization, "Bearer ") {
		return Claims{}, ErrInvalidToken
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return Claims{}, ErrInvalidToken
	}
	return v.Verify(ctx, token)
}

// Verify validates one Clerk JWT. The token must use RS256, be signed by the
// configured Clerk JWKS, and contain the configured issuer, authorized party,
// and exact owner user ID.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	if v == nil || len(token) == 0 || len(token) > maxTokenBytes {
		return Claims{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Claims{}, ErrInvalidToken
	}
	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "RS256" || strings.TrimSpace(header.KeyID) == "" {
		return Claims{}, ErrInvalidToken
	}
	payloadBytes, err := decodeSegment(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	claims, err := v.parseClaims(payloadBytes, header.KeyID)
	if err != nil {
		return Claims{}, err
	}
	key, err := v.key(ctx, header.KeyID)
	if err != nil {
		return Claims{}, err
	}
	signature, err := decodeSegment(parts[2])
	if err != nil || len(signature) == 0 {
		return Claims{}, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

func (v *Verifier) parseClaims(payload []byte, keyID string) (Claims, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil || raw == nil {
		return Claims{}, ErrInvalidToken
	}
	stringClaim := func(name string, required bool) (string, error) {
		value, ok := raw[name]
		if !ok {
			if required {
				return "", ErrInvalidToken
			}
			return "", nil
		}
		var result string
		if err := json.Unmarshal(value, &result); err != nil || strings.TrimSpace(result) == "" {
			return "", ErrInvalidToken
		}
		return result, nil
	}
	// Pending sessions have unfinished sign-in tasks and cannot authorize enrollment.
	if status, exists := raw["sts"]; exists {
		var value string
		if json.Unmarshal(status, &value) != nil || value != "active" {
			return Claims{}, ErrInvalidToken
		}
	}
	if actor, exists := raw["act"]; exists && string(actor) != "null" {
		return Claims{}, ErrInvalidToken
	}
	subject, err := stringClaim("sub", true)
	if err != nil || !sameString(subject, v.config.OwnerUserID) {
		return Claims{}, ErrInvalidToken
	}
	issuer, err := stringClaim("iss", true)
	if err != nil || !sameString(issuer, v.config.Issuer) {
		return Claims{}, ErrInvalidToken
	}
	authorizedParty, err := stringClaim("azp", true)
	if err != nil || !containsExact(v.config.AuthorizedParties, authorizedParty) {
		return Claims{}, ErrInvalidToken
	}
	audience, err := parseAudience(raw["aud"])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	if v.config.Audience != "" && !containsExact(audience, v.config.Audience) {
		return Claims{}, ErrInvalidToken
	}
	expiresAt, err := numericTime(raw, "exp", true)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	notBefore, err := numericTimePtr(raw, "nbf")
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	issuedAt, err := numericTimePtr(raw, "iat")
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	now := v.config.Now().UTC()
	if now.IsZero() || expiresAt.Add(v.config.ClockSkew).Compare(now) <= 0 {
		return Claims{}, ErrInvalidToken
	}
	if notBefore != nil && notBefore.Add(-v.config.ClockSkew).Compare(now) > 0 {
		return Claims{}, ErrInvalidToken
	}
	return Claims{
		Subject: subject, Issuer: issuer, AuthorizedParty: authorizedParty,
		Audience: audience, KeyID: keyID, ExpiresAt: expiresAt,
		NotBefore: notBefore, IssuedAt: issuedAt,
	}, nil
}

func numericTime(raw map[string]json.RawMessage, name string, required bool) (time.Time, error) {
	value, ok := raw[name]
	if !ok {
		if required {
			return time.Time{}, ErrInvalidToken
		}
		return time.Time{}, nil
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err != nil || number.String() == "" {
		return time.Time{}, ErrInvalidToken
	}
	seconds, err := number.Int64()
	if err != nil || seconds <= 0 {
		return time.Time{}, ErrInvalidToken
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func numericTimePtr(raw map[string]json.RawMessage, name string) (*time.Time, error) {
	value, ok := raw[name]
	if !ok {
		return nil, nil
	}
	parsed, err := numericTime(map[string]json.RawMessage{name: value}, name, true)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseAudience(value json.RawMessage) ([]string, error) {
	if len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(value, &single); err == nil && single != "" {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(value, &many); err != nil || len(many) == 0 {
		return nil, ErrInvalidToken
	}
	for _, item := range many {
		if item == "" {
			return nil, ErrInvalidToken
		}
	}
	return many, nil
}

func (v *Verifier) key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	for {
		now := v.config.Now().UTC()
		v.mu.Lock()
		fresh := v.keys != nil && now.Sub(v.fetchedAt) < v.config.JWKSCacheTTL
		if fresh {
			if key := v.keys[keyID]; key != nil {
				v.mu.Unlock()
				return key, nil
			}
			// A token advertising an unknown key ID must not force a JWKS
			// request for every attempt or every attacker-controlled ID.
			if !v.lastUnknownRefresh.IsZero() && now.Sub(v.lastUnknownRefresh) < v.config.UnknownKeyRetry {
				v.mu.Unlock()
				return nil, fmt.Errorf("%w: signing key %q is not published", ErrJWKSUnavailable, keyID)
			}
			v.lastUnknownRefresh = now
		} else if v.keys != nil {
			// An expired snapshot may still contain a key that was rotated
			// away. Rate-limit requests for IDs absent from that snapshot too;
			// otherwise an attacker can turn arbitrary kids into fetch storms.
			if v.keys[keyID] == nil {
				if !v.lastUnknownRefresh.IsZero() && now.Sub(v.lastUnknownRefresh) < v.config.UnknownKeyRetry {
					v.mu.Unlock()
					return nil, fmt.Errorf("%w: signing key %q is not published", ErrJWKSUnavailable, keyID)
				}
				v.lastUnknownRefresh = now
			}
		}
		if v.refreshing {
			done := v.refreshDone
			v.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: waiting for key refresh: %v", ErrJWKSUnavailable, ctx.Err())
			}
		}
		v.refreshing = true
		v.refreshDone = make(chan struct{})
		done := v.refreshDone
		v.mu.Unlock()

		keys, err := v.fetch(ctx)
		v.mu.Lock()
		if err == nil {
			v.keys = keys
			v.fetchedAt = v.config.Now().UTC()
		}
		v.refreshing = false
		close(done)
		v.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if key := keys[keyID]; key != nil {
			return key, nil
		}
		return nil, fmt.Errorf("%w: signing key %q is not published", ErrJWKSUnavailable, keyID)
	}
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	requestContext, cancel := context.WithTimeout(ctx, v.config.JWKSFetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, v.config.JWKSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v", ErrJWKSUnavailable, err)
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch: %v", ErrJWKSUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %s", ErrJWKSUnavailable, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBodyBytes+1))
	if err != nil || len(body) > maxJWKSBodyBytes {
		return nil, fmt.Errorf("%w: response too large or unreadable", ErrJWKSUnavailable)
	}
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &document); err != nil || len(document.Keys) == 0 || len(document.Keys) > maxJWKSKeys {
		return nil, fmt.Errorf("%w: invalid key set", ErrJWKSUnavailable)
	}
	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, raw := range document.Keys {
		key, kid, ok, err := parseJWK(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrJWKSUnavailable, err)
		}
		if !ok {
			continue
		}
		if _, exists := keys[kid]; exists {
			return nil, fmt.Errorf("%w: duplicate key id", ErrJWKSUnavailable)
		}
		keys[kid] = key
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no usable RSA keys", ErrJWKSUnavailable)
	}
	return keys, nil
}

func parseJWK(raw json.RawMessage) (*rsa.PublicKey, string, bool, error) {
	var jwk struct {
		KTY string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	if err := json.Unmarshal(raw, &jwk); err != nil {
		return nil, "", false, err
	}
	if jwk.KTY != "RSA" {
		return nil, "", false, nil
	}
	if jwk.Kid == "" || (jwk.Use != "" && jwk.Use != "sig") || (jwk.Alg != "" && jwk.Alg != "RS256") {
		return nil, "", false, nil
	}
	modulus, err := decodeBase64URL(jwk.N)
	if err != nil || len(modulus) == 0 {
		return nil, "", false, errors.New("invalid RSA modulus")
	}
	exponentBytes, err := decodeBase64URL(jwk.E)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
		return nil, "", false, errors.New("invalid RSA exponent")
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 | int(value)
	}
	if exponent < 2 {
		return nil, "", false, errors.New("invalid RSA exponent")
	}
	integer := new(big.Int).SetBytes(modulus)
	if integer.Sign() <= 0 {
		return nil, "", false, errors.New("invalid RSA modulus")
	}
	return &rsa.PublicKey{N: integer, E: exponent}, jwk.Kid, true, nil
}

func decodeSegment(value string) ([]byte, error) {
	return decodeBase64URL(value)
}

func decodeBase64URL(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func containsExact(values []string, wanted string) bool {
	for _, value := range values {
		if len(value) == len(wanted) && subtle.ConstantTimeCompare([]byte(value), []byte(wanted)) == 1 {
			return true
		}
	}
	return false
}

func sameString(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
