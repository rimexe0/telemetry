package enrollment

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// These tests use only a caller-provided disposable database. They are skipped
// during the normal local suite and never touch the application's configured
// database. Run with TELEMETRY_TEST_DATABASE_URL set to an isolated database.
func TestPostgresPairingLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	opened, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opened.Close)
	if err := opened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := opened.pool.Exec(ctx, `TRUNCATE telemetry_devices, telemetry_pairing_sessions`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := opened.pool.Exec(context.Background(), `TRUNCATE telemetry_devices, telemetry_pairing_sessions`); err != nil {
			t.Error(err)
		}
	})

	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	service := New(opened.pool, Options{
		PairingTTL:     time.Minute,
		MaxPending:     32,
		MaxStarts:      128,
		RateWindow:     time.Hour,
		PairingIDBytes: 32,
		Now:            func() time.Time { return clock },
	})
	secret := "collector-secret-alpha"
	hash, err := ParseCredentialHash(CredentialHash(secret))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Start(ctx, "phone-main", "Main phone", hash)
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != "pending" {
		t.Fatalf("new session status = %q", session.Status)
	}
	var storedHash []byte
	if err := opened.pool.QueryRow(ctx, `SELECT credential_hash FROM telemetry_pairing_sessions WHERE pairing_id=$1`, session.PairingID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) == secret || !equalHash(storedHash, hash) {
		t.Fatalf("stored credential is not the expected one-way hash")
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "phone-main", secret); err != nil || registered || authorized {
		t.Fatalf("pending authorization = registered:%v authorized:%v err:%v", registered, authorized, err)
	}

	// A lost start response can be retried with the same secret without
	// creating another session or consuming another device identity.
	retry, err := service.Start(ctx, "phone-main", "Main phone", hash)
	if err != nil {
		t.Fatal(err)
	}
	if retry.PairingID != session.PairingID {
		t.Fatalf("retry pairing id = %q, want %q", retry.PairingID, session.PairingID)
	}
	if _, err := service.Poll(ctx, session.PairingID, "wrong-secret"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong secret error = %v", err)
	}
	if _, err := service.Approve(ctx, session.PairingID); err != nil {
		t.Fatal(err)
	}
	approved, err := service.Poll(ctx, session.PairingID, secret)
	if err != nil || approved.Status != "approved" {
		t.Fatalf("approved poll = %+v, %v", approved, err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "phone-main", secret); err != nil || !registered || !authorized {
		t.Fatalf("active authorization = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "phone-main", "legacy-global-token"); err != nil || !registered || authorized {
		t.Fatalf("wrong active credential = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if _, err := service.Start(ctx, "phone-main", "replacement", hash); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("replacement start error = %v", err)
	}

	if _, err := service.Revoke(ctx, "phone-main"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Poll(ctx, session.PairingID, secret); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked poll error = %v", err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "phone-main", secret); err != nil || !registered || authorized {
		t.Fatalf("revoked authorization = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if _, err := service.Revoke(ctx, "phone-main"); err != nil {
		t.Fatalf("idempotent revoke error = %v", err)
	}
	devices, err := service.ListDevices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Status != "revoked" {
		t.Fatalf("device audit list = %+v, %v", devices, err)
	}
}

func TestPostgresPairingExpiryAllowsRetryAndApprovalRaceBindsOneCredential(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	opened, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opened.Close)
	if err := opened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := opened.pool.Exec(ctx, `TRUNCATE telemetry_devices, telemetry_pairing_sessions`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := opened.pool.Exec(context.Background(), `TRUNCATE telemetry_devices, telemetry_pairing_sessions`); err != nil {
			t.Error(err)
		}
	})

	clock := time.Date(2026, 9, 8, 13, 0, 0, 0, time.UTC)
	service := New(opened.pool, Options{
		PairingTTL: time.Minute,
		MaxPending: 32,
		MaxStarts:  128,
		RateWindow: time.Hour,
		Now:        func() time.Time { return clock },
	})
	firstSecret := "first-expiring-secret"
	firstHash, _ := ParseCredentialHash(CredentialHash(firstSecret))
	first, err := service.Start(ctx, "laptop-fedora", "Fedora", firstHash)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := service.Poll(ctx, first.PairingID, firstSecret); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired poll error = %v", err)
	}
	secondSecret := "second-after-expiry"
	secondHash, _ := ParseCredentialHash(CredentialHash(secondSecret))
	second, err := service.Start(ctx, "laptop-fedora", "Fedora", secondHash)
	if err != nil {
		t.Fatalf("retry after expiry = %v", err)
	}
	if second.PairingID == first.PairingID {
		t.Fatalf("expired retry reused pairing id %q", second.PairingID)
	}

	// Two collectors may race to approve different pending credentials for one
	// device ID. Both pending sessions are retained, but the unique approved
	// device row and advisory lock allow exactly one credential to win.
	firstRaceSecret := "race-secret-first"
	firstRaceHash, _ := ParseCredentialHash(CredentialHash(firstRaceSecret))
	firstRace, err := service.Start(ctx, "desktop-race", "Race desktop", firstRaceHash)
	if err != nil {
		t.Fatal(err)
	}
	secondRaceSecret := "race-secret-second"
	secondRaceHash, _ := ParseCredentialHash(CredentialHash(secondRaceSecret))
	secondRace, err := service.Start(ctx, "desktop-race", "Race desktop", secondRaceHash)
	if err != nil {
		t.Fatal(err)
	}
	const contenders = 2
	var wait sync.WaitGroup
	results := make(chan struct {
		pairingID string
		err       error
	}, contenders)
	for _, pairingID := range []string{firstRace.PairingID, secondRace.PairingID} {
		wait.Add(1)
		go func(pairingID string) {
			defer wait.Done()
			_, err := service.Approve(ctx, pairingID)
			results <- struct {
				pairingID string
				err       error
			}{pairingID: pairingID, err: err}
		}(pairingID)
	}
	wait.Wait()
	close(results)
	var winner string
	approvals := 0
	for result := range results {
		if result.err == nil {
			approvals++
			winner = result.pairingID
			continue
		}
		if !errors.Is(result.err, ErrAlreadyRegistered) {
			t.Fatalf("concurrent approval error = %v", result.err)
		}
	}
	if approvals != 1 || winner == "" {
		t.Fatalf("concurrent approvals succeeded %d times, winner = %q", approvals, winner)
	}
	devices, err := service.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].DeviceID != "desktop-race" {
		t.Fatalf("race device list = %+v", devices)
	}
	winningSecret := firstRaceSecret
	losingSecret := secondRaceSecret
	if winner == secondRace.PairingID {
		winningSecret, losingSecret = secondRaceSecret, firstRaceSecret
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "desktop-race", winningSecret); err != nil || !registered || !authorized {
		t.Fatalf("winning race credential = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "desktop-race", losingSecret); err != nil || !registered || authorized {
		t.Fatalf("losing race credential = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
}
