package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/store"
)

// The owner sets right a habit on a day already settled (owner request
// 2026-11-28: the night's teeth went unticked once bedtime had started).

func fixBody(day string) string {
	return fmt.Sprintf(`{"day":%q,"fix":true}`, day)
}

func TestFixSetsRightASettledDay(t *testing.T) {
	config := budgetPolicy(t)
	server, repository := budgetServer(t, config)
	now := time.Now().UTC()
	repository.habits = []budget.Habit{{ID: 1, Name: "teeth", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-30 * 24 * time.Hour)}}
	today := budget.DayOf(config, now)
	yesterday := budget.AddDays(today, -1)
	if _, err := store.CloseBudgetDays(context.Background(), repository, repository, now); err != nil {
		t.Fatal(err)
	}

	response := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", fixBody(yesterday))
	var state budget.State
	decodeBody(t, response, &state)
	if response.Code != http.StatusOK || state.Penalty != 0 {
		t.Fatalf("fix = %d %s", response.Code, response.Body)
	}
	event := repository.events[len(repository.events)-1]
	if event.Day != yesterday || event.Action != budget.ActionTick || event.At.Before(now) || event.Source != "device:android-1" {
		t.Fatalf("event = %+v", event)
	}
	revisions := dayRevisions(repository, yesterday)
	if last := revisions[len(revisions)-1]; len(last.HabitsDone) != 1 || len(last.HabitsMissed) != 0 || !strings.Contains(last.Cause, "set tick of habit 1 right") {
		t.Fatalf("revisions = %+v", revisions)
	}

	// Set back to not done, and the miss is back.
	undo := ownerCall(server, http.MethodPost, "/v1/habits/1/untick", fixBody(yesterday))
	decodeBody(t, undo, &state)
	if undo.Code != http.StatusOK || state.Penalty != 30 {
		t.Fatalf("fix back = %d %s", undo.Code, undo.Body)
	}

	// Any past day, not only the last week.
	month := budget.AddDays(today, -20)
	if far := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", fixBody(month)); far.Code != http.StatusOK {
		t.Fatalf("20 days back = %d %s", far.Code, far.Body)
	}
}

func TestFixRefusals(t *testing.T) {
	config := budgetPolicy(t)
	server, repository := budgetServer(t, config)
	now := time.Now().UTC()
	today := budget.DayOf(config, now)
	yesterday := budget.AddDays(today, -1)
	repository.habits = []budget.Habit{
		{ID: 1, Name: "teeth", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-30 * 24 * time.Hour)},
		{ID: 2, Name: "read", Weekdays: budget.Weekdays{}, CreatedAt: now.Add(-time.Minute)},
	}
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"today, still open", fixBody(today), http.StatusConflict},
		{"tomorrow", fixBody(budget.AddDays(today, 1)), http.StatusConflict},
		{"no day", `{"fix":true}`, http.StatusBadRequest},
		{"with at", fmt.Sprintf(`{"day":%q,"fix":true,"at":%q}`, yesterday, now.Format(time.RFC3339)), http.StatusBadRequest},
	} {
		if response := deviceCall(server, http.MethodPost, "/v1/device/habits/1/tick", test.body); response.Code != test.status {
			t.Errorf("%s: %d %s", test.name, response.Code, response.Body)
		}
	}
	if response := deviceCall(server, http.MethodPost, "/v1/device/habits/2/tick", fixBody(budget.AddDays(today, -3))); response.Code != http.StatusConflict {
		t.Errorf("before the habit existed: %d %s", response.Code, response.Body)
	}
	if len(repository.events) != 0 {
		t.Fatalf("refused fixes were kept: %+v", repository.events)
	}
}
