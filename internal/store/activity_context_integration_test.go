package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestPostgresActivityContextRulesAreRetained(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := clearIntegrationData(ctx, database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clearIntegrationData(ctx, database); err != nil {
			t.Error(err)
		}
	})
	activity, err := database.CreateActivity(ctx, Activity{Title: "Client work"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := database.CreateActivityContextRule(ctx, ActivityContextRule{ActivityID: activity.ID, Signal: ContextSignalPathPrefix, Pattern: "/work/client/"})
	if err != nil || rule.Pattern != "/work/client" {
		t.Fatalf("create = %+v, %v", rule, err)
	}
	if _, err := database.CreateActivityContextRule(ctx, ActivityContextRule{ActivityID: activity.ID, Signal: ContextSignalPathPrefix, Pattern: "/work/client"}); !errors.Is(err, ErrInvalidContextRule) {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, err := database.CreateActivityContextRule(ctx, ActivityContextRule{ActivityID: 99999, Signal: ContextSignalDomain, Pattern: "example.com"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown activity err = %v", err)
	}
	disabled, err := database.DisableActivityContextRule(ctx, rule.ID)
	if err != nil || disabled.DisabledAt == nil {
		t.Fatalf("disable = %+v, %v", disabled, err)
	}
	again, err := database.DisableActivityContextRule(ctx, rule.ID)
	if err != nil || !again.DisabledAt.Equal(*disabled.DisabledAt) {
		t.Fatalf("disable again changed the time: %+v, %v", again, err)
	}
	if enabled, _ := database.ListActivityContextRules(ctx, false); len(enabled) != 0 {
		t.Fatalf("enabled = %+v", enabled)
	}
	if all, _ := database.ListActivityContextRules(ctx, true); len(all) != 1 {
		t.Fatalf("all = %+v", all)
	}
	// The same mapping can be re-added after the old one is disabled.
	if _, err := database.CreateActivityContextRule(ctx, ActivityContextRule{ActivityID: activity.ID, Signal: ContextSignalPathPrefix, Pattern: "/work/client"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
}
