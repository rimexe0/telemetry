package budget

import (
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

// Snapshot is a budget day as it stood when it ended. Snapshots are history:
// the day can still be worked out again from the records, and a late record
// is charged to its own day.
type Snapshot struct {
	Day          string        `json:"day"`
	Budget       float64       `json:"budget"`
	Spent        float64       `json:"spent"`
	Earned       float64       `json:"earned"`
	Penalty      float64       `json:"penalty"`
	Debt         float64       `json:"debt"`
	DebtPassed   float64       `json:"debt_passed"`
	UseMinutes   float64       `json:"use_minutes"`
	HabitsDone   []int64       `json:"habits_done"`
	HabitsMissed []int64       `json:"habits_missed"`
	Settings     policy.Budget `json:"settings"`
	ClosedAt     time.Time     `json:"closed_at"`
}

// SnapshotOf is a closed day's snapshot.
func SnapshotOf(day DayLedger) Snapshot {
	return Snapshot{Day: day.Day, Budget: round(day.Budget), Spent: round(day.Spent), Earned: round(day.Earned), Penalty: round(day.Penalty),
		Debt: round(day.Debt), DebtPassed: round(day.DebtPassed), UseMinutes: round(day.UseMinutes),
		HabitsDone: append([]int64{}, day.Done...), HabitsMissed: append([]int64{}, day.Missed...), Settings: day.Settings, ClosedAt: day.End}
}

// TickDay is the budget day a habit can be ticked for now, until bedtime
// starts; false once bedtime has started.
func TickDay(config policy.Config, now time.Time) (string, bool) {
	day := DayOf(config, now)
	return day, now.Before(CloseAt(config, day))
}
