package store

import (
	"errors"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

func TestPostgresBlockingScheduleAndOpeningsApplyAndRevert(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	on := true
	windows := []policy.Window{{Start: "20:00", End: "22:00"}}
	first, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{ClosedByDefault: &on, DistractionTimes: &windows,
		AddOpenAddresses: []string{"https://www.youtube.com/watch?v=rFZHOHl-L8A"}}, "block by default")
	if err != nil || !config.ClosedByDefault || len(config.DistractionTimes) != 1 || config.OpenAddresses[0] != "youtube.com/watch?v=rFZHOHl-L8A" {
		t.Fatalf("first = %+v, %v", config, err)
	}
	later := []policy.Window{{Start: "21:00", End: "23:00"}}
	second, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{DistractionTimes: &later, RemoveOpenAddresses: []string{"youtube.com/watch?v=rFZHOHl-L8A"}}, "later")
	if err != nil || !config.ClosedByDefault || config.DistractionTimes[0].Start != "21:00" || len(config.OpenAddresses) != 0 {
		t.Fatalf("second = %+v, %v", config, err)
	}
	bad := []policy.Window{{Start: "8pm", End: "22:00"}}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{DistractionTimes: &bad}, "x"); !errors.Is(err, ErrInvalidAgentChange) {
		t.Fatalf("bad time = %v", err)
	}
	if _, err := database.RevertAgentChange(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ := database.LatestPolicy(ctx)
	if !stored.Config.ClosedByDefault || stored.Config.DistractionTimes[0].Start != "20:00" || len(stored.Config.OpenAddresses) != 1 {
		t.Fatalf("after reverting the second = %+v", stored.Config)
	}
	if _, err := database.RevertAgentChange(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if stored, _ = database.LatestPolicy(ctx); stored.Config.ClosedByDefault || len(stored.Config.DistractionTimes) != 0 || len(stored.Config.OpenAddresses) != 0 {
		t.Fatalf("after reverting the first = %+v", stored.Config)
	}

	now := time.Now().UTC()
	opened, opening, err := database.OpenNow(ctx, Opening{Purpose: "a tutorial", Sites: []string{"https://www.YouTube.com/watch?v=x"}, Until: now.Add(20 * time.Minute)}, "a tutorial")
	if err != nil || opening.Sites[0] != "youtube.com" {
		t.Fatalf("open = %+v, %v", opening, err)
	}
	if _, _, err := database.OpenNow(ctx, Opening{Purpose: "nothing named", Until: now.Add(time.Minute)}, "x"); !errors.Is(err, ErrInvalidAgentChange) {
		t.Fatalf("an opening with no target = %v", err)
	}
	active, _ := database.ActiveOpenings(ctx, now)
	groups := PolicyOpenings(policy.Config{Distractions: []policy.TargetGroup{{ID: "video", Domains: []string{"youtube.com"}}, {ID: "feed", Domains: []string{"x.com"}}}}, active)
	if len(active) != 1 || len(groups) != 1 || len(groups[0].GroupIDs) != 1 || groups[0].GroupIDs[0] != "video" {
		t.Fatalf("active = %+v groups = %+v", active, groups)
	}
	if expired, _ := database.ActiveOpenings(ctx, now.Add(21*time.Minute)); len(expired) != 0 {
		t.Fatalf("after its time = %+v", expired)
	}
	if _, err := database.RevertAgentChange(ctx, opened.ID); err != nil {
		t.Fatal(err)
	}
	if active, _ = database.ActiveOpenings(ctx, now); len(active) != 0 {
		t.Fatalf("reverting must end the opening: %+v", active)
	}
}

func TestPostgresOpeningRuleWaitsCapsAndCounts(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	rule := policy.OpeningRule{WaitMinutes: 10, MaxMinutes: 30, PerDay: 1}
	if _, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{OpeningRule: &rule}, "the owner's numbers"); err != nil || config.OpeningRule != rule {
		t.Fatalf("rule = %+v, %v", config.OpeningRule, err)
	}
	now := time.Now().UTC()
	if _, _, err := database.OpenNow(ctx, Opening{Purpose: "too long", Everything: true, Until: now.Add(time.Hour)}, "x"); !errors.Is(err, ErrOpeningRefused) {
		t.Fatalf("over the limit = %v", err)
	}
	_, opening, err := database.OpenNow(ctx, Opening{Purpose: "a call", Everything: true, Until: now.Add(20 * time.Minute)}, "a call")
	if err != nil || opening.StartsAt.Sub(now) < 9*time.Minute || opening.Until.Sub(opening.StartsAt).Round(time.Minute) != 20*time.Minute {
		t.Fatalf("opening = %+v, %v", opening, err)
	}
	if _, _, err := database.OpenNow(ctx, Opening{Purpose: "another", Everything: true, Until: now.Add(5 * time.Minute)}, "x"); !errors.Is(err, ErrOpeningRefused) {
		t.Fatalf("over the day's count = %v", err)
	}
	// Cancelled during its wait, it does not count.
	if _, err := database.EndOpening(ctx, opening.ID); err != nil {
		t.Fatal(err)
	}
	if used, err := database.OpeningsSince(ctx, now.Add(-time.Hour)); err != nil || used != 0 {
		t.Fatalf("used = %d, %v", used, err)
	}
	// A looser rule waits for the next day.
	looser := policy.OpeningRule{}
	if _, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{OpeningRule: &looser}, "x"); err != nil || config.OpeningRuleAt(now) != rule || config.OpeningRuleNext == nil {
		t.Fatalf("looser = %+v, %v", config, err)
	}
	// So does one saved as a whole policy.
	stored, _ := database.LatestPolicy(ctx)
	whole := stored.Config
	whole.OpeningRule, whole.OpeningRuleNext, whole.OpeningRuleNextFrom = policy.OpeningRule{}, nil, ""
	if saved, err := database.SavePolicy(ctx, whole, "x"); err != nil || saved.Config.OpeningRuleAt(now) != rule {
		t.Fatalf("saved = %+v, %v", saved.Config, err)
	}
}

func TestPostgresMorningCodes(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	first, err := database.CreateMorningCode(ctx)
	if err != nil || len(first) < 40 {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, _ := database.CreateMorningCode(ctx)
	if ok, err := database.ScanMorningCode(ctx, first); ok || err != nil {
		t.Fatalf("a retired code opened the morning: %v", err)
	}
	if ok, err := database.ScanMorningCode(ctx, second); !ok || err != nil {
		t.Fatalf("the current code: %v", err)
	}
	if scanned, err := database.LatestMorningScan(ctx, time.Now().Add(time.Minute)); err != nil || scanned == nil {
		t.Fatalf("scan = %v, %v", scanned, err)
	}
	if made, err := database.MorningCodeMade(ctx); err != nil || made == nil {
		t.Fatalf("made = %v, %v", made, err)
	}
}

func TestPostgresOpeningNeedsItsConfirmations(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	rule := policy.OpeningRule{Confirmations: 2, ConfirmSeconds: 1}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{OpeningRule: &rule}, "the owner's numbers"); err != nil {
		t.Fatal(err)
	}
	_, opening, err := database.OpenNow(ctx, Opening{Purpose: "a call", Everything: true, Until: time.Now().Add(20 * time.Minute)}, "a call")
	if err != nil || opening.Confirmed() || opening.NextConfirmAt == nil {
		t.Fatalf("opening = %+v, %v", opening, err)
	}
	active, _ := database.ActiveOpenings(ctx, time.Now())
	if len(PolicyOpenings(policy.Config{}, active)) != 0 {
		t.Fatalf("an unconfirmed opening opened something: %+v", active)
	}
	if _, err := database.ConfirmOpening(ctx, opening.ID); !errors.Is(err, ErrOpeningRefused) {
		t.Fatalf("a confirmation before its time = %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if opening, err = database.ConfirmOpening(ctx, opening.ID); err != nil || opening.Confirmed() {
		t.Fatalf("first = %+v, %v", opening, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if opening, err = database.ConfirmOpening(ctx, opening.ID); err != nil || !opening.Confirmed() || opening.Until.Sub(opening.StartsAt).Round(time.Minute) != 20*time.Minute {
		t.Fatalf("last = %+v, %v", opening, err)
	}
	if active, _ = database.ActiveOpenings(ctx, time.Now()); len(PolicyOpenings(policy.Config{}, active)) != 1 {
		t.Fatalf("a confirmed opening did not open: %+v", active)
	}
}

func TestPostgresSettingsLockRefusesLooseningUntilThePin(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	on := true
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{ClosedByDefault: &on}, "closed"); err != nil {
		t.Fatal(err)
	}
	lock := policy.SettingsLock{Enabled: true, UnlockMinutes: 10}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{SettingsLock: &lock}, "lock"); !errors.Is(err, ErrInvalidAgentChange) {
		t.Fatalf("a lock without a PIN = %v", err)
	}
	if err := database.SetSettingsPin(ctx, "", "482913"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{SettingsLock: &lock}, "lock"); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{ClosedByDefault: &off}, "open"); !errors.Is(err, ErrSettingsLocked) {
		t.Fatalf("switching off while locked = %v", err)
	}
	if _, _, err := database.OpenNow(ctx, Opening{Purpose: "x", Everything: true, Until: time.Now().Add(time.Minute)}, "x"); !errors.Is(err, ErrSettingsLocked) {
		t.Fatalf("an opening while locked = %v", err)
	}
	if _, err := database.ApplyBlocklistChange(ctx, []policy.BlocklistChange{{Action: "unlist", AndroidPackage: "com.example.feed"}}, "x"); !errors.Is(err, ErrSettingsLocked) {
		t.Fatalf("unblocking while locked = %v", err)
	}
	if _, err := database.ApplyBlocklistChange(ctx, []policy.BlocklistChange{{Action: "block", AndroidPackage: "com.example.feed"}}, "x"); err != nil {
		t.Fatalf("blocking more while locked = %v", err)
	}
	if err := database.SetSettingsPin(ctx, "000000", "111111"); !errors.Is(err, ErrSettingsLocked) {
		t.Fatalf("changing the PIN without the current one = %v", err)
	}
	if _, err := database.UnlockSettings(ctx, "123456"); !errors.Is(err, ErrSettingsLocked) {
		t.Fatalf("a wrong PIN = %v", err)
	}
	// Two wrong tries so far; the right PIN still works.
	if _, err := database.UnlockSettings(ctx, "482913"); err != nil {
		t.Fatal(err)
	}
	if _, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{ClosedByDefault: &off}, "open"); err != nil || config.ClosedByDefault {
		t.Fatalf("after the PIN = %+v, %v", config.ClosedByDefault, err)
	}
}

func TestPostgresCorrectingAJustSavedBedtimeAppliesAtOnce(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	now := time.Now().UTC()
	strict := policy.Bedtime{Enabled: true, Start: now.Add(2 * time.Hour).In(time.FixedZone("TR", 3*3600)).Format("15:04"), End: now.Add(20 * time.Hour).In(time.FixedZone("TR", 3*3600)).Format("15:04")}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &strict}, "mistake"); err != nil {
		t.Fatal(err)
	}
	off := policy.Bedtime{}
	preview, err := database.PreviewBlockingSchedule(ctx, ScheduleChange{Bedtime: &off})
	if err != nil || preview.BedtimeNextFrom != "" {
		t.Fatalf("a correction would wait: %+v, %v", preview.BedtimeNext, err)
	}
	if _, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &off}, "fix"); err != nil || config.BedtimeAt(now).Enabled {
		t.Fatalf("correction = %+v, %v", config.Bedtime, err)
	}
}
