package main

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

// Clerk is opt-in and single-owner. Empty owner ID permits only sign-in setup;
// the caller must deny all owner API access until an exact ID is configured.
func validateClerkConfig(key, issuer, origin string) error {
	if key == "" {
		if issuer != "" {
			return errors.New("Clerk issuer requires a publishable key")
		}
		return nil
	}
	encoded := strings.TrimPrefix(strings.TrimPrefix(key, "pk_test_"), "pk_live_")
	if encoded == key {
		return errors.New("invalid Clerk publishable key prefix")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil || !strings.HasSuffix(string(raw), "$") {
		return errors.New("invalid Clerk publishable key")
	}
	host := strings.TrimSuffix(string(raw), "$")
	if issuer != "https://"+host {
		return errors.New("Clerk issuer must match publishable key frontend host")
	}
	for _, value := range []string{issuer, origin} {
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || strings.ContainsAny(value, " ;\r\n\t") {
			return errors.New("Clerk issuer and public URL must be origins without paths")
		}
		if u.Scheme != "https" {
			return errors.New("Clerk issuer and public URL require HTTPS")
		}
	}
	return nil
}
