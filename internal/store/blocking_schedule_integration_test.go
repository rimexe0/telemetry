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
	// A looser rule waits for the next day, once the rule is past the
	// minutes in which changing it back counts as a correction.
	aged, err := database.LatestPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	aged.Config.RuleChanged = map[string]time.Time{"opening_rule": now.Add(-time.Hour)}
	if _, err := database.SavePolicy(ctx, aged.Config, "an hour later"); err != nil {
		t.Fatal(err)
	}
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

// Tightening a rule a little must not open a window to switch it off at once.
func TestPostgresACorrectionNeverAsksLessThanBefore(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	now := time.Now().UTC()
	zone := time.FixedZone("TR", 3*3600)
	first := policy.Bedtime{Enabled: true, Start: now.Add(2 * time.Hour).In(zone).Format("15:04"), End: now.Add(10 * time.Hour).In(zone).Format("15:04")}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &first}, "bedtime"); err != nil {
		t.Fatal(err)
	}
	tighter := first
	tighter.WindDownMinutes = 5
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &tighter}, "a little more"); err != nil {
		t.Fatal(err)
	}
	// Back to the first bedtime is a correction.
	if _, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &first}, "undo"); err != nil || config.BedtimeNextFrom != "" || config.BedtimeAt(now).WindDownMinutes != 0 {
		t.Fatalf("undo = %+v next %+v, %v", config.Bedtime, config.BedtimeNext, err)
	}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &tighter}, "a little more again"); err != nil {
		t.Fatal(err)
	}
	// Off asks less than the first bedtime: it waits for the next day.
	off := policy.Bedtime{}
	_, config, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &off}, "off")
	if err != nil {
		t.Fatal(err)
	}
	if !config.BedtimeAt(now).Enabled || config.BedtimeNext == nil || config.BedtimeNextFrom == "" {
		t.Fatalf("switching off applied at once: %+v next %+v from %q", config.Bedtime, config.BedtimeNext, config.BedtimeNextFrom)
	}
}

func TestPostgresTheExtensionRuleStaysOn(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	on := policy.ExtensionRule{Enabled: true, QuietSeconds: 30}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Extension: &on}, "on"); err != nil {
		t.Fatal(err)
	}
	off := policy.ExtensionRule{}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Extension: &off}, "off"); !errors.Is(err, ErrInvalidAgentChange) {
		t.Fatalf("switching the extension rule off = %v", err)
	}
	latest, err := database.LatestPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := latest.Config
	config.Extension, config.ExtensionNext, config.ExtensionNextFrom = policy.ExtensionRule{}, nil, ""
	if _, err := database.SavePolicy(ctx, config, "whole policy without it"); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("saving a policy without the extension rule = %v", err)
	}
}

// Every accepted loosening is recorded for an agent to look at; changes
// close together are one case, and a preview records nothing.
func TestPostgresLooseningOpensALoopholeCase(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	on := policy.Bedtime{Enabled: true, Start: "00:00", End: "08:00"}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &on}, "bedtime on"); err != nil {
		t.Fatal(err)
	}
	if cases, err := database.LoopholeCases(ctx, false, 10); err != nil || len(cases) != 0 {
		t.Fatalf("tightening opened a case: %+v, %v", cases, err)
	}
	// Past the minutes in which switching it off would be a correction.
	aged, _ := database.LatestPolicy(ctx)
	aged.Config.RuleChanged = map[string]time.Time{"bedtime": time.Now().UTC().Add(-48 * time.Hour)}
	if _, err := database.SavePolicy(ctx, aged.Config, "two days later"); err != nil {
		t.Fatal(err)
	}
	off := policy.Bedtime{}
	if _, err := database.PreviewBlockingSchedule(ctx, ScheduleChange{Bedtime: &off}); err != nil {
		t.Fatal(err)
	}
	if cases, _ := database.LoopholeCases(ctx, false, 10); len(cases) != 0 {
		t.Fatalf("a preview opened a case: %+v", cases)
	}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{Bedtime: &off}, "bedtime off at 23:59"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{AddOpenAddresses: []string{"youtube.com/watch?v=x"}}, "one page"); err != nil {
		t.Fatal(err)
	}
	cases, err := database.LoopholeCases(ctx, true, 10)
	if err != nil || len(cases) != 1 || len(cases[0].Events) != 2 || cases[0].Status() != "waiting" {
		t.Fatalf("cases = %+v, %v", cases, err)
	}
	if cases[0].Events[0].Reason != "bedtime off at 23:59" || cases[0].Events[0].Source != "schedule" {
		t.Fatalf("event = %+v", cases[0].Events[0])
	}
	// Handed over only once quiet for LoopholeSettle.
	if due, _ := database.LoopholesToDispatch(ctx, time.Now().UTC()); len(due) != 0 {
		t.Fatal("handed over before it settled")
	}
	due, err := database.LoopholesToDispatch(ctx, time.Now().UTC().Add(LoopholeSettle+time.Minute))
	if err != nil || len(due) != 1 {
		t.Fatalf("due = %+v, %v", due, err)
	}
	id := due[0].ID
	if err := database.MarkLoopholeDispatched(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	// A loosening after the handover starts a new case.
	times := []policy.Window{{Start: "12:00", End: "13:00"}}
	if _, _, err := database.ApplyBlockingSchedule(ctx, ScheduleChange{DistractionTimes: &times}, "lunch break"); err != nil {
		t.Fatal(err)
	}
	if cases, _ := database.LoopholeCases(ctx, true, 10); len(cases) != 2 {
		t.Fatalf("cases after the handover = %d", len(cases))
	}
	if _, err := database.ReportLoophole(ctx, id, "", "x", "", ""); err == nil {
		t.Fatal("an empty report was stored")
	}
	reported, err := database.ReportLoophole(ctx, id, "Bedtime was switched off a minute before it began.", "Bedtime went off at 23:59; the fix makes that wait for the morning.", "https://github.com/rimexe0/telemetry/pull/1", "")
	if err != nil || reported.Status() != "reported" || reported.MergeAfter == nil || reported.MergeAfter.Sub(*reported.ReportedAt).Round(time.Minute) != LoopholeMergeAfter {
		t.Fatalf("reported = %+v, %v", reported, err)
	}
	answered, err := database.AnswerLoophole(ctx, id, "don't merge, I needed it")
	if err != nil || answered.Status() != "answered" || answered.ReminderDue(time.Now().UTC()) {
		t.Fatalf("answered = %+v, %v", answered, err)
	}
	if _, err := database.ReportLoophole(ctx, id, "again", "again", "", ""); err == nil {
		t.Fatal("a report replaced the owner's answer")
	}
	if done, err := database.CloseLoophole(ctx, id, LoopholeDropped, "the owner said no"); err != nil || done.Status() != "closed" {
		t.Fatalf("closed = %+v, %v", done, err)
	}
	if err := database.SetLoopholeWebhook(ctx, "http://insecure"); err == nil {
		t.Fatal("an http webhook was saved")
	}
	if err := database.SetLoopholeWebhook(ctx, "https://example.com/hook"); err != nil {
		t.Fatal(err)
	}
	if webhook, err := database.LoopholeWebhook(ctx); err != nil || webhook != "https://example.com/hook" {
		t.Fatalf("webhook = %q, %v", webhook, err)
	}
}
