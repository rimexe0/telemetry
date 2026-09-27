package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rimexe0/telemetry/internal/policy"
)

func TestPostgresPolicyVersionsAreAppendOnly(t *testing.T) {
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
	if latest, err := database.LatestPolicy(ctx); err != nil || latest != nil {
		t.Fatalf("empty latest = %+v, %v", latest, err)
	}
	first, err := database.SavePolicy(ctx, policy.Config{Distractions: []policy.TargetGroup{{ID: "social", Domains: []string{"Feed.Example.com"}}}}, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.SavePolicy(ctx, policy.Config{EmergencyMinutes: 15}, "second")
	if err != nil || second.ID <= first.ID {
		t.Fatalf("second = %+v, %v", second, err)
	}
	latest, err := database.LatestPolicy(ctx)
	if err != nil || latest.ID != second.ID || latest.Config.EmergencyMinutes != 15 || latest.Config.Timezone != "Europe/Istanbul" {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	if first.Config.Distractions[0].Domains[0] != "feed.example.com" {
		t.Fatalf("domain not normalised: %+v", first.Config)
	}
	if _, err := database.SavePolicy(ctx, policy.Config{EmergencyMinutes: 999}, ""); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid err = %v", err)
	}
	var count int
	if err := database.pool.QueryRow(ctx, `SELECT COUNT(*) FROM policy_versions`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("versions = %d, %v", count, err)
	}
}
