package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/store"
)

// A made-up place for tests, not the owner's.
const testRule = `{"kind": "place", "place": {"name": "Test pier", "lat": 41.0, "lon": 29.0}, "window": {"from": "wake", "until": "%s"}}`

func pierFix(at time.Time, meters float64) places.Fix {
	return places.Fix{At: at.UTC(), Lat: 41.0 + meters/111_195, Lon: 29.0, Accuracy: 8}
}

func TestHabitRuleIsSetShownOnEveryRouteAndRemoved(t *testing.T) {
	server, repository := budgetServer(t, budgetPolicy(t))
	repository.habits = []budget.Habit{{ID: 1, Name: "Going outside", Weekdays: budget.Weekdays{}, CreatedAt: time.Now().UTC().Add(-72 * time.Hour)}}

	set := deviceCall(server, http.MethodPatch, "/v1/device/habits/1", `{"rule": `+fmt.Sprintf(testRule, "12:00")+`}`)
	var written struct {
		Habit budget.Habit `json:"habit"`
	}
	decodeBody(t, set, &written)
	rule := written.Habit.Rule
	if set.Code != http.StatusOK || rule == nil || rule.Kind != "place" || rule.RadiusM != 100 || rule.Minutes != 1 || rule.Window.From != "wake" {
		t.Fatalf("set = %d %s", set.Code, set.Body)
	}
	if !strings.Contains(repository.why[len(repository.why)-1], "on the phone") {
		t.Fatalf("logged as %q", repository.why)
	}

	// The owner's list, the device's list and the day's budget all carry it.
	var owner struct {
		Habits []budget.Habit `json:"habits"`
	}
	decodeBody(t, ownerCall(server, http.MethodGet, "/v1/habits", ""), &owner)
	if len(owner.Habits) != 1 || owner.Habits[0].Rule == nil || owner.Habits[0].Rule.Place.Name != "Test pier" {
		t.Fatalf("owner list = %+v", owner.Habits)
	}
	device := deviceCall(server, http.MethodGet, "/v1/device/habits", "")
	if !strings.Contains(device.Body.String(), `"rule":{"kind":"place"`) {
		t.Fatalf("device list = %s", device.Body)
	}
	var now struct {
		Budget budget.State `json:"budget"`
	}
	decodeBody(t, deviceGet(server, "android-1", "device-secret"), &now)
	if len(now.Budget.Habits) != 1 || now.Budget.Habits[0].Rule == nil {
		t.Fatalf("device now habits = %+v", now.Budget.Habits)
	}

	// Leaving the rule out keeps it; a rename does not touch it.
	deviceCall(server, http.MethodPatch, "/v1/device/habits/1", `{"name": "Outside"}`)
	if repository.habits[0].Rule == nil || repository.habits[0].Name != "Outside" {
		t.Fatalf("after a rename = %+v", repository.habits[0])
	}
	// A rule that can't be worked out is refused with the reason.
	bad := deviceCall(server, http.MethodPatch, "/v1/device/habits/1", `{"rule": {"kind": "place", "window": {"from": "wake", "until": "12:00"}}}`)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "needs a place") {
		t.Fatalf("bad rule = %d %s", bad.Code, bad.Body)
	}
	// null removes it.
	removed := ownerCall(server, http.MethodPatch, "/v1/habits/1", `{"rule": null}`)
	if removed.Code != http.StatusOK || repository.habits[0].Rule != nil {
		t.Fatalf("removed = %d %s", removed.Code, removed.Body)
	}
	var shape map[string]map[string]any
	json.Unmarshal(removed.Body.Bytes(), &shape)
	if value, ok := shape["habit"]["rule"]; !ok || value != nil {
		t.Fatalf("a habit without a rule says rule: null, got %s", removed.Body)
	}
}

func TestRuleTickFromThePhoneLeavesTheOwnersWord(t *testing.T) {
	config := budgetPolicy(t)
	server, repository := budgetServer(t, config)
	rule := budget.HabitRule{Kind: "place", Place: &budget.RulePlace{Name: "Test pier", Lat: 41, Lon: 29}, Window: budget.RuleWindow{From: "wake", Until: "12:00"}}
	repository.habits = []budget.Habit{
		{ID: 1, Name: "Going outside", Weekdays: budget.Weekdays{}, CreatedAt: time.Now().UTC().Add(-72 * time.Hour), Rule: &rule},
		{ID: 2, Name: "No rule", Weekdays: budget.Weekdays{}, CreatedAt: time.Now().UTC().Add(-72 * time.Hour)},
	}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	day := budget.DayOf(config, at)
	body := fmt.Sprintf(`{"day":%q,"at":%q,"by":"rule"}`, day, at.Format(time.RFC3339Nano))

	ticked := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", body)
	var state budget.State
	decodeBody(t, ticked, &state)
	if ticked.Code != http.StatusOK || len(repository.events) != 1 || repository.events[0].Source != "rule:device:android-1" || !repository.events[0].At.Equal(at) {
		t.Fatalf("rule tick = %d %s, events %+v", ticked.Code, ticked.Body, repository.events)
	}
	if !state.Habits[0].Done || state.Habits[0].DoneBy != "rule" || state.Habits[0].DoneAt == nil || !state.Habits[0].DoneAt.Equal(at) {
		t.Fatalf("state after the rule tick = %+v", state.Habits[0])
	}

	// The owner unticks it: the rule never ticks it again that day.
	untick := deviceCall(server, http.MethodPost, "/v1/device/habits/1/untick", "{}")
	if untick.Code != http.StatusOK {
		t.Fatalf("untick = %d %s", untick.Code, untick.Body)
	}
	again := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", fmt.Sprintf(`{"day":%q,"at":%q,"by":"rule"}`, day, at.Add(30*time.Second).Format(time.RFC3339Nano)))
	decodeBody(t, again, &state)
	if again.Code != http.StatusOK || len(repository.events) != 2 || state.Habits[0].Done || !state.Habits[0].Unticked {
		t.Fatalf("rule after untick = %d %s, events %+v", again.Code, again.Body, repository.events)
	}

	// A habit without a rule is not ticked by one; a rule never unticks.
	norule := deviceCall(server, http.MethodPost, "/v1/device/habits/2/tick", body)
	if norule.Code != http.StatusOK || len(repository.events) != 2 {
		t.Fatalf("no rule = %d, events %+v", norule.Code, repository.events)
	}
	if refused := deviceCall(server, http.MethodPost, "/v1/device/habits/1/untick", body); refused.Code != http.StatusBadRequest {
		t.Fatalf("rule untick = %d", refused.Code)
	}
	if refused := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", `{"by":"rule"}`); refused.Code != http.StatusBadRequest {
		t.Fatalf("rule tick without at = %d", refused.Code)
	}
}

func TestServerTicksARuleFromStoredPositions(t *testing.T) {
	config := budgetPolicy(t) // bedtime off: the day starts at 04:00
	_, repository := budgetServer(t, config)
	location, _ := time.LoadLocation("Europe/Istanbul")
	// Three hours into today's budget day, so the test reads the same at any hour.
	now := budget.DayStart(config, budget.DayOf(config, time.Now())).Add(3 * time.Hour)
	until := now.Add(time.Hour).In(location).Format("15:04")
	var rule budget.HabitRule
	if err := json.Unmarshal([]byte(fmt.Sprintf(testRule, until)), &rule); err != nil || rule.Normalize() != nil {
		t.Fatal(err)
	}
	rule.Minutes = 2
	repository.habits = []budget.Habit{{ID: 1, Name: "Going outside", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-72 * time.Hour), Rule: &rule}}
	ctx := context.Background()

	// No positions: nothing is ticked on a guess.
	if ticked, err := store.EvaluateHabitRules(ctx, repository, repository, now); err != nil || ticked != 0 || len(repository.events) != 0 {
		t.Fatalf("no positions = %d, %v", ticked, err)
	}
	// Near but not inside.
	repository.fixes = []places.Fix{pierFix(now.Add(-40*time.Minute), 300), pierFix(now.Add(-38*time.Minute), 250), pierFix(now.Add(-36*time.Minute), 200)}
	if ticked, err := store.EvaluateHabitRules(ctx, repository, repository, now); err != nil || ticked != 0 {
		t.Fatalf("near = %d, %v", ticked, err)
	}
	// Two minutes at the place.
	repository.fixes = append(repository.fixes, pierFix(now.Add(-30*time.Minute), 30), pierFix(now.Add(-29*time.Minute), 20), pierFix(now.Add(-28*time.Minute), 10),
		pierFix(now.Add(-20*time.Minute), 400))
	ticked, err := store.EvaluateHabitRules(ctx, repository, repository, now)
	if err != nil || ticked != 1 || len(repository.events) != 1 {
		t.Fatalf("reached = %d, %v, events %+v", ticked, err, repository.events)
	}
	event := repository.events[0]
	if event.Source != "rule:server" || !event.At.Equal(now.Add(-28*time.Minute)) || event.Action != budget.ActionTick || event.Day != budget.DayOf(config, now) {
		t.Fatalf("event = %+v", event)
	}
	// Checked again, it is not ticked twice.
	if ticked, err := store.EvaluateHabitRules(ctx, repository, repository, now.Add(5*time.Minute)); err != nil || ticked != 0 || len(repository.events) != 1 {
		t.Fatalf("again = %d, %v", ticked, err)
	}
	// Unticked by the owner, it stays unticked.
	if err := store.TickHabit(ctx, repository, repository, 1, budget.ActionUntick, "", "owner", now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ticked, err := store.EvaluateHabitRules(ctx, repository, repository, now.Add(10*time.Minute)); err != nil || ticked != 0 || len(repository.events) != 2 {
		t.Fatalf("after the owner's untick = %d, %v", ticked, err)
	}
}
