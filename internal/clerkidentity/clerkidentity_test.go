package clerkidentity

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifyAcceptsClerkClaimsAndCachesJWKS(t *testing.T) {
	key, verifier, requests := testVerifier(t)
	claims := validClaims(time.Unix(1_800_000_000, 0))
	token := signToken(t, key, claims)

	first, err := verifier.VerifyAuthorization(t.Context(), "Bearer "+token)
	if err != nil {
		t.Fatalf("first verification: %v", err)
	}
	second, err := verifier.Verify(t.Context(), token)
	if err != nil {
		t.Fatalf("cached verification: %v", err)
	}
	if first.Subject != "user_owner" || second.AuthorizedParty != "https://telemetry.example" {
		t.Fatalf("claims = %+v %+v", first, second)
	}
	if got := atomic.LoadInt32(requests); got != 1 {
		t.Fatalf("jwks requests = %d, want 1", got)
	}
}

func TestVerifyRejectsForgedSignature(t *testing.T) {
	_, verifier, _ := testVerifier(t)
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// The token has valid claims but is signed by a different key while
	// advertising the owner's key ID.
	_, err = verifier.Verify(t.Context(), signToken(t, attacker, validClaims(time.Unix(1_800_000_000, 0))))
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged token error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsWrongOwner(t *testing.T) {
	key, verifier, _ := testVerifier(t)
	claims := validClaims(time.Unix(1_800_000_000, 0))
	claims["sub"] = "user_other"
	_, err := verifier.Verify(t.Context(), signToken(t, key, claims))
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong owner error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	key, verifier, _ := testVerifier(t)
	claims := validClaims(time.Unix(1_800_000_000, 0))
	claims["iss"] = "https://attacker.example"
	_, err := verifier.Verify(t.Context(), signToken(t, key, claims))
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong issuer error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsWrongAuthorizedPartyAndAudience(t *testing.T) {
	key, verifier, _ := testVerifier(t)
	for name, mutate := range map[string]func(map[string]any){
		"authorized party": func(claims map[string]any) { claims["azp"] = "https://attacker.example" },
		"audience":         func(claims map[string]any) { claims["aud"] = "other-service" },
	} {
		t.Run(name, func(t *testing.T) {
			claims := validClaims(time.Unix(1_800_000_000, 0))
			mutate(claims)
			_, err := verifier.Verify(t.Context(), signToken(t, key, claims))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestVerifyRejectsExpiredAndNotYetValidTokens(t *testing.T) {
	key, verifier, _ := testVerifier(t)
	for name, mutate := range map[string]func(map[string]any){
		"expired": func(claims map[string]any) { claims["exp"] = 1_699_999_999 },
		"not before": func(claims map[string]any) {
			claims["nbf"] = 1_800_000_001
		},
	} {
		t.Run(name, func(t *testing.T) {
			claims := validClaims(time.Unix(1_800_000_000, 0))
			mutate(claims)
			_, err := verifier.Verify(t.Context(), signToken(t, key, claims))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestVerifyRefreshesForAnUnknownKeyID(t *testing.T) {
	first, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		count := atomic.AddInt32(&requests, 1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		if count == 1 {
			_, _ = writer.Write([]byte(jwksJSON("first", first)))
			return
		}
		_, _ = writer.Write([]byte(jwksJSON("first", first, "second", second)))
	}))
	defer server.Close()
	verifier, err := New(Config{
		JWKSURL: server.URL, Issuer: "https://owner.clerk.accounts.dev",
		AuthorizedParties: []string{"https://telemetry.example"}, OwnerUserID: "user_owner",
		Audience: "telemetry", Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(t.Context(), signTokenWithID(t, first, "first", validClaims(time.Unix(1_800_000_000, 0)))); err != nil {
		t.Fatalf("first key verification: %v", err)
	}
	if _, err := verifier.Verify(t.Context(), signTokenWithID(t, second, "second", validClaims(time.Unix(1_800_000_000, 0)))); err != nil {
		t.Fatalf("rotated key verification: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("jwks requests = %d, want refresh after unknown kid", got)
	}
}

func TestVerifyRateLimitsUnknownKeyRefreshes(t *testing.T) {
	key, verifier, requests := testVerifier(t)
	claims := validClaims(time.Unix(1_800_000_000, 0))
	// Prime the cache with the published owner's key.
	if _, err := verifier.Verify(t.Context(), signToken(t, key, claims)); err != nil {
		t.Fatalf("prime verification: %v", err)
	}
	for _, kid := range []string{"forged-a", "forged-b"} {
		if _, err := verifier.Verify(t.Context(), signTokenWithID(t, key, kid, claims)); !errors.Is(err, ErrJWKSUnavailable) {
			t.Fatalf("kid %q error = %v, want ErrJWKSUnavailable", kid, err)
		}
	}
	if got := atomic.LoadInt32(requests); got != 2 {
		t.Fatalf("jwks requests = %d, want one initial fetch plus one bounded refresh", got)
	}
}

func testVerifier(t *testing.T) (*rsa.PrivateKey, *Verifier, *int32) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&requests, 1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(jwksJSON("owner-key", key)))
	}))
	t.Cleanup(server.Close)
	verifier, err := New(Config{
		JWKSURL: server.URL, Issuer: "https://owner.clerk.accounts.dev",
		AuthorizedParties: []string{"https://telemetry.example"}, OwnerUserID: "user_owner",
		Audience: "telemetry", Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return key, verifier, &requests
}

func validClaims(expires time.Time) map[string]any {
	return map[string]any{
		"sub": "user_owner", "iss": "https://owner.clerk.accounts.dev",
		"azp": "https://telemetry.example", "aud": "telemetry",
		"iat": expires.Unix() - 60, "nbf": expires.Unix() - 60, "exp": expires.Unix() + 300,
	}
}

func signToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	return signTokenWithID(t, key, "owner-key", claims)
}

func signTokenWithID(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := encodeJSON(t, map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload := encodeJSON(t, claims)
	input := header + "." + payload
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, 5, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func jwksJSON(values ...any) string {
	keys := make([]map[string]string, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		kid := values[index].(string)
		key := values[index+1].(*rsa.PrivateKey)
		exponent := big.NewInt(int64(key.PublicKey.E)).Bytes()
		keys = append(keys, map[string]string{
			"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(exponent),
		})
	}
	data, _ := json.Marshal(map[string]any{"keys": keys})
	return string(data)
}

func TestRejectsPendingAndImpersonatedSessions(t *testing.T) {
	key, verifier, _ := testVerifier(t)
	for _, field := range []string{"sts", "act"} {
		claims := validClaims(time.Unix(1_800_000_060, 0))
		if field == "sts" {
			claims[field] = "pending"
		} else {
			claims[field] = map[string]any{"sub": "user_other"}
		}
		if _, err := verifier.Verify(t.Context(), signToken(t, key, claims)); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
}
