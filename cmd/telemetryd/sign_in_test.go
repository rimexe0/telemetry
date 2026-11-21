package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rimexe0/telemetry/internal/signin"
)

type resetStore struct {
	settings signin.Settings
	err      error
	changes  []string
}

func (s *resetStore) Load(context.Context) (signin.Settings, error) { return s.settings, s.err }

func (s *resetStore) Update(_ context.Context, change string, mutate func(signin.Settings) (signin.Settings, error)) (signin.Settings, error) {
	if s.err != nil {
		return signin.Settings{}, s.err
	}
	next, err := mutate(s.settings)
	if err != nil {
		return signin.Settings{}, err
	}
	if err := next.Validate(); err != nil {
		return signin.Settings{}, err
	}
	s.settings, s.changes = next, append(s.changes, change)
	return next, nil
}

func (s *resetStore) Changes(context.Context, int) ([]signin.Change, error) { return nil, nil }

func TestResetSignInTurnsThePanelTokenOn(t *testing.T) {
	key := "pk_test_" + base64.StdEncoding.EncodeToString([]byte("fond-owl-12.clerk.accounts.dev$"))
	store := &resetStore{settings: signin.Settings{ClerkEnabled: true, Clerk: signin.Clerk{PublishableKey: key, Issuer: "https://fond-owl-12.clerk.accounts.dev", OwnerUserID: "user_owner"}}}
	var out bytes.Buffer
	env := map[string]string{"TELEMETRY_PANEL_TOKEN": "t", "TELEMETRY_PANEL_SESSION_SECRET": "s"}
	if err := runResetSignIn(t.Context(), store, &out, func(name string) string { return env[name] }); err != nil {
		t.Fatal(err)
	}
	if !store.settings.PanelTokenEnabled || !store.settings.ClerkEnabled || len(store.changes) != 1 || !strings.Contains(store.changes[0], "-reset-sign-in") {
		t.Fatalf("reset %+v %v", store.settings, store.changes)
	}
	if !strings.Contains(out.String(), "Panel token sign-in is on again. Clerk sign-in is still on for user_owner.") || strings.Contains(out.String(), "Note:") {
		t.Fatalf("output %q", out.String())
	}
	out.Reset()
	env["TELEMETRY_CLERK_PUBLISHABLE_KEY"] = key
	if err := runResetSignIn(t.Context(), store, &out, func(name string) string { return env[name] }); err != nil || !strings.Contains(out.String(), "ignores these settings") {
		t.Fatalf("environment note: %q %v", out.String(), err)
	}
}

func TestResetSignInBeforeMigrations(t *testing.T) {
	var out bytes.Buffer
	store := &resetStore{err: &pgconn.PgError{Code: "42P01"}}
	if err := runResetSignIn(t.Context(), store, &out, func(string) string { return "" }); err != nil || !strings.Contains(out.String(), "already on") {
		t.Fatalf("%q %v", out.String(), err)
	}
}
