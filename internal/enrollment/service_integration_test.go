package enrollment

import (
	"context"
	"errors"
	"os"
	"strings"
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
	if _, err := opened.pool.Exec(ctx, `TRUNCATE telemetry_devices, telemetry_pairing_sessions, telemetry_pairing_invites, telemetry_device_credential_history`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := opened.pool.Exec(context.Background(), `TRUNCATE telemetry_devices, telemetry_pairing_sessions, telemetry_pairing_invites, telemetry_device_credential_history`); err != nil {
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

	// The removed device is told so, but only with its own key.
	if !service.Removed(ctx, "phone-main", secret) {
		t.Fatal("removed device with its own key was not reported as removed")
	}
	if service.Removed(ctx, "phone-main", "someone-else") {
		t.Fatal("removed reported for a key that was never the device's")
	}

	// Set up again (a wiped phone keeps its device ID): it asks again, is
	// marked as returning, and approval replaces its key and keeps the record.
	newSecret := "collector-secret-beta"
	newHash, _ := ParseCredentialHash(CredentialHash(newSecret))
	again, err := service.Start(ctx, "phone-main", "Main phone", newHash)
	if err != nil || again.Status != "pending" {
		t.Fatalf("pairing again = %+v, %v", again, err)
	}
	pending, err := service.ListPending(ctx)
	if err != nil || len(pending) != 1 || !pending[0].Returning {
		t.Fatalf("returning device pending list = %+v, %v", pending, err)
	}
	if _, err := service.Approve(ctx, again.PairingID); err != nil {
		t.Fatalf("approve returning device = %v", err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "phone-main", newSecret); err != nil || !registered || !authorized {
		t.Fatalf("new key = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "phone-main", secret); err != nil || !registered || authorized {
		t.Fatalf("replaced key = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if service.Removed(ctx, "phone-main", secret) {
		t.Fatal("an active device was reported as removed")
	}
	var replaced int
	if err := opened.pool.QueryRow(ctx, `SELECT COUNT(*) FROM telemetry_device_credential_history WHERE device_id='phone-main' AND previous_status='revoked' AND previous_pairing_id=$1`, session.PairingID).Scan(&replaced); err != nil || replaced != 1 {
		t.Fatalf("replaced key history = %d, %v", replaced, err)
	}
	devices, err = service.ListDevices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Status != "active" || devices[0].PairingID != again.PairingID {
		t.Fatalf("device after pairing again = %+v, %v", devices, err)
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
	if _, err := opened.pool.Exec(ctx, `TRUNCATE telemetry_devices, telemetry_pairing_sessions, telemetry_pairing_invites, telemetry_device_credential_history`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := opened.pool.Exec(context.Background(), `TRUNCATE telemetry_devices, telemetry_pairing_sessions, telemetry_pairing_invites, telemetry_device_credential_history`); err != nil {
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
	// device ID. The owner approved both, so both succeed one after the other,
	// and the device ends up with exactly one working credential.
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
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent approval error = %v", result.err)
		}
	}
	devices0, err := service.ListDevices(ctx)
	if err != nil || len(devices0) != 1 {
		t.Fatalf("race device list = %+v, %v", devices0, err)
	}
	winner := devices0[0].PairingID
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

func TestPostgresSetupCodes(t *testing.T) {
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
	reset := func() {
		if _, err := opened.pool.Exec(context.Background(), `TRUNCATE telemetry_devices, telemetry_pairing_sessions, telemetry_pairing_invites, telemetry_device_credential_history`); err != nil {
			t.Error(err)
		}
	}
	reset()
	t.Cleanup(reset)
	clock := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	service := New(opened.pool, Options{MaxOpenInvites: 3, Now: func() time.Time { return clock }})

	invite, err := service.CreateInvite(ctx)
	if err != nil || invite.Status != "open" || len(invite.Code) != 9 || invite.Code[4] != '-' {
		t.Fatalf("invite = %+v, %v", invite, err)
	}
	var stored []byte
	if err := opened.pool.QueryRow(ctx, `SELECT code_hash FROM telemetry_pairing_invites WHERE invite_id=$1`, invite.InviteID).Scan(&stored); err != nil || strings.Contains(string(stored), invite.Code[:4]) {
		t.Fatalf("stored code = %x, %v", stored, err)
	}
	secret := "phone-secret-with-code"
	hash, _ := ParseCredentialHash(CredentialHash(secret))
	if _, err := service.StartWithCode(ctx, "android-1", "Phone", hash, "ZZZZ-ZZZZ"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code error = %v", err)
	}
	// Typed loosely: lower case, a space instead of the dash.
	typed := strings.ToLower(strings.Replace(invite.Code, "-", " ", 1))
	session, err := service.StartWithCode(ctx, "android-1", "Phone", hash, typed)
	if err != nil || session.Status != "approved" {
		t.Fatalf("start with code = %+v, %v", session, err)
	}
	if registered, authorized, err := service.AuthorizeDevice(ctx, "android-1", secret); err != nil || !registered || !authorized {
		t.Fatalf("code-paired device = registered:%v authorized:%v err:%v", registered, authorized, err)
	}
	if polled, err := service.Poll(ctx, session.PairingID, secret); err != nil || polled.Status != "approved" {
		t.Fatalf("poll after code = %+v, %v", polled, err)
	}
	// The answer was lost and the phone asks again with the same key and code.
	retry, err := service.StartWithCode(ctx, "android-1", "Phone", hash, invite.Code)
	if err != nil || retry.Status != "approved" || retry.PairingID != session.PairingID {
		t.Fatalf("retry with used code = %+v, %v", retry, err)
	}
	otherHash, _ := ParseCredentialHash(CredentialHash("someone-else"))
	if _, err := service.StartWithCode(ctx, "laptop-x", "Laptop", otherHash, invite.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second use of a code = %v", err)
	}
	invites, err := service.ListInvites(ctx)
	if err != nil || len(invites) != 1 || invites[0].Status != "used" || invites[0].UsedByDevice != "android-1" || invites[0].Code != "" {
		t.Fatalf("invites after use = %+v, %v", invites, err)
	}

	expiring, _ := service.CreateInvite(ctx)
	cancelled, _ := service.CreateInvite(ctx)
	if err := service.CancelInvite(ctx, cancelled.InviteID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartWithCode(ctx, "laptop-x", "Laptop", otherHash, cancelled.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("cancelled code = %v", err)
	}
	if _, err := service.CreateInvite(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInvite(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInvite(ctx); !errors.Is(err, ErrInviteLimit) {
		t.Fatalf("open invite limit = %v", err)
	}
	clock = clock.Add(2 * time.Hour)
	if _, err := service.StartWithCode(ctx, "laptop-x", "Laptop", otherHash, expiring.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired code = %v", err)
	}

	// Guessing stops working after a burst of wrong codes.
	for i := 0; i < codeFailureAllowed; i++ {
		_, _ = service.StartWithCode(ctx, "guesser", "Guess", otherHash, "0000-0000")
	}
	fresh, _ := service.CreateInvite(ctx)
	if _, err := service.StartWithCode(ctx, "guesser", "Guess", otherHash, fresh.Code); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after guesses = %v", err)
	}
	clock = clock.Add(codeFailureWindow)
	if s, err := service.StartWithCode(ctx, "guesser", "Guess", otherHash, fresh.Code); err != nil || s.Status != "approved" {
		t.Fatalf("after the guess window = %+v, %v", s, err)
	}

	if err := service.SetRelayURL(ctx, "http://relay.example"); !errors.Is(err, ErrInvalidRelayURL) {
		t.Fatalf("plain http relay = %v", err)
	}
	if err := service.SetRelayURL(ctx, "https://relay.example/"); err != nil {
		t.Fatal(err)
	}
	if got, err := service.RelayURL(ctx); err != nil || got != "https://relay.example" {
		t.Fatalf("relay = %q, %v", got, err)
	}
}

func TestPostgresFormattedPhoneContinuesAsTheSamePhone(t *testing.T) {
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
	reset := func() {
		if _, err := opened.pool.Exec(context.Background(), `TRUNCATE telemetry_devices, telemetry_pairing_sessions, telemetry_pairing_invites, telemetry_device_credential_history`); err != nil {
			t.Error(err)
		}
	}
	reset()
	t.Cleanup(reset)
	clock := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	service := New(opened.pool, Options{Now: func() time.Time { return clock }})
	pair := func(id, name, secret string) PairingSession {
		t.Helper()
		invite, err := service.CreateInvite(ctx)
		if err != nil {
			t.Fatal(err)
		}
		hash, _ := ParseCredentialHash(CredentialHash(secret))
		session, err := service.StartWithCode(ctx, id, name, hash, invite.Code)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	first := pair("android-1111111111111111", "Nothing A063", "first-install")
	if first.DeviceID != "android-1111111111111111" {
		t.Fatalf("first install = %+v", first)
	}
	// The same phone, still sending: a second phone of the same model is not merged into it.
	if _, _, err := service.AuthorizeDevice(ctx, "android-1111111111111111", "first-install"); err != nil {
		t.Fatal(err)
	}
	var seen *time.Time
	if err := opened.pool.QueryRow(ctx, `SELECT last_seen_at FROM telemetry_devices WHERE device_id='android-1111111111111111'`).Scan(&seen); err != nil || seen == nil {
		t.Fatalf("last seen not recorded: %v %v", seen, err)
	}
	twin := pair("android-2222222222222222", "Nothing A063", "twin")
	if twin.DeviceID != "android-2222222222222222" {
		t.Fatalf("a phone in use was taken over: %+v", twin)
	}
	// Formatted: quiet for an hour, then set up again with a new Android ID.
	clock = clock.Add(time.Hour)
	if _, err := opened.pool.Exec(ctx, `UPDATE telemetry_devices SET last_seen_at=$1 WHERE device_id='android-2222222222222222'`, clock); err != nil {
		t.Fatal(err)
	}
	hash, _ := ParseCredentialHash(CredentialHash("after-format"))
	pending, err := service.Start(ctx, "android-3333333333333333", "Nothing A063", hash)
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.ListPending(ctx)
	if err != nil || len(list) != 1 || list[0].Continues != "android-1111111111111111" {
		t.Fatalf("pending continues = %+v, %v", list, err)
	}
	approved, err := service.Approve(ctx, pending.PairingID)
	if err != nil || approved.DeviceID != "android-1111111111111111" {
		t.Fatalf("approved as = %+v, %v", approved, err)
	}
	if again, err := service.Approve(ctx, pending.PairingID); err != nil || again.DeviceID != "android-1111111111111111" {
		t.Fatalf("approved twice = %+v, %v", again, err)
	}
	polled, err := service.Poll(ctx, pending.PairingID, "after-format")
	if err != nil || polled.DeviceID != "android-1111111111111111" || polled.Status != "approved" {
		t.Fatalf("poll tells the phone its ID = %+v, %v", polled, err)
	}
	if registered, authorized, _ := service.AuthorizeDevice(ctx, "android-1111111111111111", "after-format"); !registered || !authorized {
		t.Fatal("the continued phone's new key does not work")
	}
	if registered, authorized, _ := service.AuthorizeDevice(ctx, "android-1111111111111111", "first-install"); !registered || authorized {
		t.Fatal("the formatted phone's old key still works")
	}
	if registered, _, _ := service.AuthorizeDevice(ctx, "android-3333333333333333", "after-format"); registered {
		t.Fatal("the new Android ID was registered as its own device")
	}
	// A computer keeps its own ID even with a matching name.
	desktop := pair("desktop-abc", "Nothing A063", "desktop")
	if desktop.DeviceID != "desktop-abc" {
		t.Fatalf("desktop = %+v", desktop)
	}
}
