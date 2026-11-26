package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// Ticks made on the phone while the server could not be reached count for
// the day they were made, and correct a day that had already closed.

func lateBody(day string, at time.Time) string {
	return fmt.Sprintf(`{"day":%q,"at":%q}`, day, at.UTC().Format(time.RFC3339Nano))
}

func dayRevisions(repository *budgetRepository, day string) []budget.Snapshot {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []budget.Snapshot{}
	for _, snapshot := range repository.days {
		if snapshot.Day == day {
			result = append(result, snapshot)
		}
	}
	return result
}

func TestLateTickCountsForItsDayAndLiftsTheNextDaysPenalty(t *testing.T) {
	config := budgetPolicy(t)
	server, repository := budgetServer(t, config)
	now := time.Now().UTC()
	repository.habits = []budget.Habit{{ID: 1, Name: "walk", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-72 * time.Hour)}}
	today := budget.DayOf(config, now)
	yesterday := budget.AddDays(today, -1)

	// Yesterday closed with the walk missed: today pays for it.
	var before struct {
		Budget budget.State `json:"budget"`
	}
	decodeBody(t, deviceGet(server, "android-1", "device-secret"), &before)
	if before.Budget.Penalty != 30 {
		t.Fatalf("penalty before = %v", before.Budget.Penalty)
	}
	// No phone has ever delivered, so the days settle when they end.
	if _, err := store.CloseBudgetDays(context.Background(), repository, repository, now); err != nil {
		t.Fatal(err)
	}
	if first := dayRevisions(repository, yesterday); len(first) != 1 || first[0].Revision != 1 || len(first[0].HabitsMissed) != 1 {
		t.Fatalf("yesterday's snapshot = %+v", first)
	}

	// The walk was ticked on the phone yesterday while the server could not
	// be reached, and arrives now.
	at := budget.DayStart(config, yesterday).Add(2 * time.Hour).Truncate(time.Millisecond)
	response := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", lateBody(yesterday, at))
	var state budget.State
	decodeBody(t, response, &state)
	if response.Code != http.StatusOK || state.Day != today || state.Penalty != 0 {
		t.Fatalf("late tick = %d %s", response.Code, response.Body)
	}
	event := repository.events[len(repository.events)-1]
	if event.Day != yesterday || !event.At.Equal(at) || event.ReceivedAt.Before(now) || event.Source != "device:android-1" {
		t.Fatalf("event = %+v", event)
	}

	// Yesterday has a corrected revision; the one written at its close stays.
	revisions := dayRevisions(repository, yesterday)
	if len(revisions) != 2 || revisions[0].Revision != 1 || len(revisions[0].HabitsMissed) != 1 || len(revisions[0].HabitsDone) != 0 {
		t.Fatalf("revisions = %+v", revisions)
	}
	if revisions[1].Revision != 2 || len(revisions[1].HabitsDone) != 1 || len(revisions[1].HabitsMissed) != 0 || revisions[1].Earned != 30 ||
		!strings.Contains(revisions[1].Cause, "late tick of habit 1") {
		t.Fatalf("revision 2 = %+v", revisions[1])
	}
	var overview struct {
		Days []budget.Snapshot `json:"days"`
	}
	decodeBody(t, ownerCall(server, http.MethodGet, "/v1/budget?days=30", ""), &overview)
	found := false
	for _, day := range overview.Days {
		if day.Day == yesterday {
			found = true
			if day.Revision != 2 {
				t.Fatalf("the budget shows revision %d of yesterday", day.Revision)
			}
		}
	}
	if !found {
		t.Fatalf("days = %+v", overview.Days)
	}

	// The phone delivering it again changes nothing.
	again := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", lateBody(yesterday, at))
	if again.Code != http.StatusOK || len(repository.events) != 1 || len(dayRevisions(repository, yesterday)) != 2 {
		t.Fatalf("delivered again = %d, %d events", again.Code, len(repository.events))
	}

	// A late untick undoes that day's tick, and the penalty is back.
	undo := ownerCall(server, http.MethodPost, "/v1/habits/1/untick", lateBody(yesterday, at.Add(time.Hour)))
	decodeBody(t, undo, &state)
	if undo.Code != http.StatusOK || state.Penalty != 30 || len(dayRevisions(repository, yesterday)) != 3 {
		t.Fatalf("late untick = %d %s", undo.Code, undo.Body)
	}
}

func TestLateTickRefusals(t *testing.T) {
	config := budgetPolicy(t)
	server, repository := budgetServer(t, config)
	now := time.Now().UTC()
	today := budget.DayOf(config, now)
	yesterday := budget.AddDays(today, -1)
	at := budget.DayStart(config, yesterday).Add(2 * time.Hour)
	date, _ := time.Parse("2006-01-02", yesterday)
	other := (int(date.Weekday()) + 3) % 7
	repository.habits = []budget.Habit{
		{ID: 1, Name: "walk", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-30 * 24 * time.Hour)},
		{ID: 2, Name: "read", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-time.Minute)},
		{ID: 3, Name: "swim", Weekdays: budget.Weekdays{other}, CreatedAt: now.Add(-30 * 24 * time.Hour)},
	}
	for _, test := range []struct {
		name, path, body string
		status           int
		code             string
	}{
		{"day is not the day at falls in", "/v1/device/habits/1/tick", lateBody(today, at), http.StatusBadRequest, "invalid_day"},
		{"a day that is no day", "/v1/device/habits/1/tick", lateBody("2026-13-40", at), http.StatusBadRequest, "invalid_day"},
		{"at is no time", "/v1/device/habits/1/tick", `{"day":"` + yesterday + `","at":"yesterday morning"}`, http.StatusBadRequest, "invalid_at"},
		{"at is ahead of the clock", "/v1/device/habits/1/tick", fmt.Sprintf(`{"at":%q}`, now.Add(10*time.Minute).Format(time.RFC3339)), http.StatusBadRequest, "invalid_at"},
		{"more than a week back", "/v1/device/habits/1/tick", lateBody(budget.AddDays(today, -8), budget.DayStart(config, budget.AddDays(today, -8)).Add(2*time.Hour)), http.StatusConflict, "day_closed"},
		{"the habit did not exist then", "/v1/device/habits/2/tick", lateBody(yesterday, at), http.StatusConflict, "not_scheduled"},
		{"the habit was not on that weekday", "/v1/device/habits/3/untick", lateBody(yesterday, at), http.StatusConflict, "not_scheduled"},
		{"no such habit", "/v1/habits/99/tick", lateBody(yesterday, at), http.StatusNotFound, "not_found"},
	} {
		var response *httptest.ResponseRecorder
		if strings.HasPrefix(test.path, "/v1/device/") {
			response = deviceCall(server, http.MethodPost, test.path, test.body)
		} else {
			response = ownerCall(server, http.MethodPost, test.path, test.body)
		}
		// Refusals are {error, message}; bad requests {error: {code, detail}}.
		var body struct {
			Error json.RawMessage `json:"error"`
		}
		decodeBody(t, response, &body)
		code := ""
		if json.Unmarshal(body.Error, &code) != nil {
			var nested struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(body.Error, &nested)
			code = nested.Code
		}
		if response.Code != test.status || code != test.code {
			t.Errorf("%s: %d %s", test.name, response.Code, response.Body)
		}
	}
	if len(repository.events) != 0 {
		t.Fatalf("refused ticks were kept: %+v", repository.events)
	}
	// A day of the week before is still taken.
	week := budget.AddDays(today, -7)
	if response := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", lateBody(week, budget.DayStart(config, week).Add(time.Hour))); response.Code != http.StatusOK {
		t.Fatalf("a week back = %d %s", response.Code, response.Body)
	}
}

func TestLateTickAfterBedtimeStartedIsRefused(t *testing.T) {
	config := budgetPolicy(t)
	config.Bedtime = policy.Bedtime{Enabled: true, Start: "22:00", End: "07:00"}
	server, repository := budgetServer(t, config)
	now := time.Now().UTC()
	repository.habits = []budget.Habit{{ID: 1, Name: "walk", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-30 * 24 * time.Hour)}}
	yesterday := budget.AddDays(budget.DayOf(config, now), -1)
	bedtime, ok := budget.BedtimeStart(config, yesterday)
	if !ok {
		t.Fatal("yesterday has no bedtime")
	}
	late := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", lateBody(yesterday, bedtime.Add(30*time.Minute)))
	var refusal map[string]string
	decodeBody(t, late, &refusal)
	if late.Code != http.StatusConflict || refusal["error"] != "day_closed" || refusal["message"] != store.ErrHabitLateClosed.Error() {
		t.Fatalf("a tick made in bedtime = %d %v", late.Code, refusal)
	}
	if inTime := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", lateBody(yesterday, bedtime.Add(-30*time.Minute))); inTime.Code != http.StatusOK {
		t.Fatalf("a tick made before bedtime = %d %s", inTime.Code, inTime.Body)
	}
}

func TestDeviceHabitRoutes(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	now := time.Now().UTC()
	archived := now.Add(-time.Hour)
	repository.habits = []budget.Habit{
		{ID: 1, Name: "walk", Position: 1, Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-72 * time.Hour)},
		{ID: 2, Name: "read", Position: 0, Weekdays: budget.Weekdays{1, 3}, CreatedAt: now.Add(-72 * time.Hour)},
		{ID: 3, Name: "old", Position: 2, Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-72 * time.Hour), ArchivedAt: &archived},
	}
	repository.reasons = []string{"to make music"}
	for _, path := range []string{"/v1/device/habits", "/v1/device/habits/heatmap?days=30"} {
		anonymous := httptest.NewRecorder()
		server.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a device credential = %d", path, anonymous.Code)
		}
		wrong := httptest.NewRequest(http.MethodGet, path, nil)
		wrong.Header.Set("X-Telemetry-Device-ID", "android-1")
		wrong.Header.Set("Authorization", "Bearer wrong")
		refused := httptest.NewRecorder()
		server.ServeHTTP(refused, wrong)
		if refused.Code != http.StatusUnauthorized {
			t.Fatalf("%s with a wrong token = %d", path, refused.Code)
		}
	}

	response := deviceCall(server, http.MethodGet, "/v1/device/habits", "")
	var raw map[string][]json.RawMessage
	decodeBody(t, response, &raw)
	if response.Code != http.StatusOK || len(raw["habits"]) != 2 || len(raw["reasons"]) != 1 {
		t.Fatalf("habits = %d %s", response.Code, response.Body)
	}
	var first map[string]any
	if err := json.Unmarshal(raw["habits"][0], &first); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "name", "position", "weekdays", "created_at", "archived_at"} {
		if _, ok := first[key]; !ok {
			t.Errorf("a habit is missing %q", key)
		}
	}
	var listed struct {
		Habits []deviceHabit `json:"habits"`
	}
	decodeBody(t, response, &listed)
	if listed.Habits[0].ID != 2 || fmt.Sprint(listed.Habits[0].Weekdays) != "[1 3]" || fmt.Sprint(listed.Habits[1].Weekdays) != "[0 1 2 3 4 5 6]" || listed.Habits[1].ArchivedAt != nil {
		t.Fatalf("habits = %+v", listed.Habits)
	}

	if short := deviceCall(server, http.MethodGet, "/v1/device/habits/heatmap?days=3", ""); short.Code != http.StatusBadRequest {
		t.Fatalf("days=3 = %d", short.Code)
	}
	if long := deviceCall(server, http.MethodGet, "/v1/device/habits/heatmap?days=401", ""); long.Code != http.StatusBadRequest {
		t.Fatalf("days=401 = %d", long.Code)
	}
	device := deviceCall(server, http.MethodGet, "/v1/device/habits/heatmap?days=30", "")
	owner := ownerCall(server, http.MethodGet, "/v1/habits/heatmap?days=30", "")
	if device.Code != http.StatusOK || device.Body.String() != owner.Body.String() {
		t.Fatalf("device heatmap = %d %s\nowner heatmap = %s", device.Code, device.Body, owner.Body)
	}
	var grid budget.Heatmap
	decodeBody(t, device, &grid)
	if len(grid.Habits) != 3 {
		t.Fatalf("heatmap = %+v", grid)
	}
}

// phoneUse is distraction use on the phone from start for length.
func phoneUse(start time.Time, length time.Duration) []activity.Record {
	return []activity.Record{
		{Source: "android:phone:usage", Kind: "android.usage.activity_resumed", Start: start, Payload: json.RawMessage(`{"package_name":"com.google.android.youtube"}`)},
		{Source: "android:phone:usage", Kind: "android.usage.activity_paused", Start: start.Add(length), Payload: json.RawMessage(`{"package_name":"com.google.android.youtube"}`)},
	}
}

func TestSilentPhoneDayWaitsToSettle(t *testing.T) {
	config := budgetPolicy(t)
	_, repository := budgetServer(t, config)
	ctx, now := context.Background(), time.Now().UTC()
	repository.habits = []budget.Habit{{ID: 1, Name: "walk", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-30 * 24 * time.Hour)}}
	today := budget.DayOf(config, now)
	yesterday, twoBack, threeBack := budget.AddDays(today, -1), budget.AddDays(today, -2), budget.AddDays(today, -3)
	// Two days back the owner borrowed from yesterday.
	repository.records = phoneUse(budget.DayStart(config, twoBack).Add(time.Hour), 3*time.Hour)
	// The phone last delivered during that day, and nothing since.
	phone := budget.DayStart(config, twoBack).Add(5 * time.Hour)
	repository.phone = &phone

	if _, err := store.CloseBudgetDays(ctx, repository, repository, now); err != nil {
		t.Fatal(err)
	}
	if day, _ := repository.BudgetDay(ctx, threeBack); day == nil {
		t.Fatal("the day before the phone went silent was not settled")
	}
	for _, day := range []string{twoBack, yesterday} {
		if snapshot, _ := repository.BudgetDay(ctx, day); snapshot != nil {
			t.Fatalf("%s was settled while the phone was silent: %+v", day, snapshot)
		}
	}
	// Meanwhile what the waiting day passes on is worked out from the records.
	waiting, err := store.LoadBudgetInputs(ctx, repository, repository, config, yesterday, now)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.DebtIn <= 0 {
		t.Fatalf("debt from the waiting day = %v", waiting.DebtIn)
	}

	// The phone delivers again: the days settle.
	delivered := now
	repository.phone = &delivered
	if written, err := store.CloseBudgetDays(ctx, repository, repository, now); err != nil || written != 2 {
		t.Fatalf("written = %d, %v", written, err)
	}
	settled, _ := repository.BudgetDay(ctx, twoBack)
	if settled == nil || settled.DebtPassed != waiting.DebtIn {
		t.Fatalf("two days back = %+v, it passed on %v while waiting", settled, waiting.DebtIn)
	}
}

func TestSilentPhoneDaySettlesAfterTwoDays(t *testing.T) {
	config := budgetPolicy(t)
	_, repository := budgetServer(t, config)
	ctx, now := context.Background(), time.Now().UTC()
	twoBack := budget.AddDays(budget.DayOf(config, now), -2)
	phone := budget.DayStart(config, twoBack).Add(time.Hour)
	repository.phone = &phone
	end := budget.DayEnd(config, twoBack)
	if _, err := store.CloseBudgetDays(ctx, repository, repository, end.Add(47*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if day, _ := repository.BudgetDay(ctx, twoBack); day != nil {
		t.Fatal("settled before two days of silence")
	}
	if _, err := store.CloseBudgetDays(ctx, repository, repository, end.Add(48*time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if day, _ := repository.BudgetDay(ctx, twoBack); day == nil {
		t.Fatal("not settled after two days of silence")
	}
}
