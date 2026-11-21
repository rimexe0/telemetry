package signin

import (
	"os"
	"testing"
)

func TestPostgresStoreKeepsHistoryAndResets(t *testing.T) {
	address := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if address == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := t.Context()
	store, err := OpenStore(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM telemetry_settings WHERE key = $1`, settingsKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM telemetry_settings_changes WHERE key = $1`, settingsKey); err != nil {
		t.Fatal(err)
	}
	if settings, err := store.Load(ctx); err != nil || settings != Defaults() {
		t.Fatalf("empty load: %+v %v", settings, err)
	}
	linked := Clerk{PublishableKey: testKey, Issuer: testIssuer, OwnerUserID: "user_owner"}
	if _, err := store.Update(ctx, "link", func(s Settings) (Settings, error) { s.Clerk = linked; return s, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "Clerk only", func(s Settings) (Settings, error) { s.ClerkEnabled, s.PanelTokenEnabled = true, false; return s, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ctx, "nothing", func(s Settings) (Settings, error) { s.ClerkEnabled = false; return s, nil }); err == nil {
		t.Fatal("saved settings with no way in")
	}
	settings, err := Reset(ctx, store)
	if err != nil || !settings.PanelTokenEnabled || !settings.ClerkEnabled {
		t.Fatalf("reset %+v %v", settings, err)
	}
	loaded, err := store.Load(ctx)
	if err != nil || loaded != settings {
		t.Fatalf("load after reset %+v %v", loaded, err)
	}
	changes, err := store.Changes(ctx, 10)
	if err != nil || len(changes) != 3 {
		t.Fatalf("changes %+v %v", changes, err)
	}
	if changes[0].Previous.PanelTokenEnabled || !changes[0].Value.PanelTokenEnabled || changes[2].Previous != Defaults() || changes[2].Change != "link" {
		t.Fatalf("history does not keep what was replaced: %+v", changes)
	}
}
