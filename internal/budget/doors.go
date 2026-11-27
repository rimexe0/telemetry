package budget

import (
	"errors"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

const (
	DoorWaiting   = "waiting"
	DoorOpen      = "open"
	DoorGrace     = "grace"
	DoorEnded     = "ended"
	DoorCancelled = "cancelled"
)

// Door is the owner's way to open distractions at more than 1x: "Still want
// it" after seeing the cost, a wait, then a window that is charged. It is
// the agreed way out, so it is never a loophole case.
type Door struct {
	ID          int64     `json:"id"`
	RequestedAt time.Time `json:"requested_at"`
	OpensAt     time.Time `json:"opens_at"`
	// WindowEnd is the most the door lasts: the window from opening, cut at
	// a distraction time's start, or the distraction time's end for a door
	// made inside one.
	WindowEnd time.Time `json:"window_end"`
	// InDistractionTime is a door made inside a distraction time to borrow:
	// it has no window allowance.
	InDistractionTime bool `json:"in_distraction_time"`
	// Borrowing is true when nothing of the day was left as it was made.
	Borrowing bool `json:"borrowing"`
	// UseMinutes and GraceMinutes are the allowance as the door was made,
	// so a later change to the settings does not move an open door.
	UseMinutes   int        `json:"use_minutes"`
	GraceMinutes int        `json:"grace_minutes"`
	CancelledAt  *time.Time `json:"cancelled_at,omitempty"`
	Source       string     `json:"source,omitempty"`
}

// DoorBack is a door screen left with Back: the owner returned.
type DoorBack struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Source string    `json:"source,omitempty"`
}

// DoorView is a door as devices show it.
type DoorView struct {
	ID          int64     `json:"id"`
	State       string    `json:"state"`
	RequestedAt time.Time `json:"requested_at"`
	OpensAt     time.Time `json:"opens_at"`
	WindowEnd   time.Time `json:"window_end"`
	// EndsAt is the sliding end.
	EndsAt      time.Time `json:"ends_at"`
	UsedMinutes float64   `json:"used_minutes"`
	// UseMinutes is the window allowance; null inside a distraction time.
	UseMinutes *int `json:"use_minutes"`
	Borrowing  bool `json:"borrowing"`
}

// Active reports whether the door is waiting or open now.
func (v DoorView) Active() bool {
	return v.State == DoorWaiting || v.State == DoorOpen || v.State == DoorGrace
}

// ErrDoorUnavailable is a door that cannot be made now.
var ErrDoorUnavailable = errors.New("no door now")

// UnavailableError says why there is no door, in the owner's words.
type UnavailableError struct{ Reason string }

func (e UnavailableError) Error() string { return e.Reason }
func (e UnavailableError) Unwrap() error { return ErrDoorUnavailable }

// Unavailable is ErrDoorUnavailable with the reason as its message.
func Unavailable(reason string) error { return UnavailableError{Reason: reason} }

// view works out where a door stands now and its sliding end.
func (c *calc) view(door Door, today DayLedger) DoorView {
	view := DoorView{ID: door.ID, RequestedAt: door.RequestedAt, OpensAt: door.OpensAt, WindowEnd: door.WindowEnd, Borrowing: door.Borrowing}
	if !door.InDistractionTime {
		use := door.UseMinutes
		view.UseMinutes = &use
	}
	if door.CancelledAt != nil {
		view.State, view.EndsAt = DoorCancelled, *door.CancelledAt
		return view
	}
	now := c.now
	used := useMinutes(c.spans, door.OpensAt, earlier(now, door.WindowEnd))
	view.UsedMinutes = round(used)
	start := later(now, door.OpensAt)
	end := door.WindowEnd
	grace := float64(door.GraceMinutes)
	inGrace := false
	if !door.InDistractionTime {
		allowance := float64(door.UseMinutes) + grace
		if used >= allowance {
			reached, _ := useReached(c.spans, door.OpensAt, door.WindowEnd, allowance)
			end = earlier(end, reached)
		} else {
			end = earlier(end, start.Add(time.Duration((allowance-used)*float64(time.Minute))))
		}
		inGrace = used >= float64(door.UseMinutes)
	}
	if today.Left+today.Borrowable > 0 {
		paid := runOut(c.pieces, start, end, today.Left, today.Borrowable, today.Settings.BorrowMultiplier, anyRate)
		if paid.Before(end) {
			end = earlier(end, paid.Add(time.Duration(grace*float64(time.Minute))))
		}
	} else {
		// Nothing left to spend or borrow: only the grace to close or save
		// something remains, counted from when it ran out.
		from := door.OpensAt
		if today.ExhaustedAt != nil {
			from = later(from, *today.ExhaustedAt)
		}
		if reached, ok := useReached(c.spans, from, now, grace); ok {
			end = earlier(end, reached)
		} else {
			end = earlier(end, start.Add(time.Duration((grace-useMinutes(c.spans, from, now))*float64(time.Minute))))
		}
		inGrace = true
	}
	view.EndsAt = end
	switch {
	case !now.Before(end):
		view.State = DoorEnded
	case now.Before(door.OpensAt):
		view.State = DoorWaiting
	case inGrace:
		view.State = DoorGrace
	default:
		view.State = DoorOpen
	}
	return view
}

// NewDoor is the door "Still want it" makes now, or ErrDoorUnavailable with
// the reason in state.DoorUnavailableReason.
func NewDoor(config policy.Config, state State, now time.Time) (Door, error) {
	if !state.DoorAvailable {
		return Door{}, Unavailable(state.DoorUnavailableReason)
	}
	settings := config.BudgetAt(now)
	door := Door{RequestedAt: now.UTC(), OpensAt: now.UTC().Add(time.Duration(settings.WaitMinutes) * time.Minute),
		Borrowing: state.Left <= 0, UseMinutes: settings.WindowUseMinutes, GraceMinutes: settings.GraceMinutes}
	clock := func(t time.Time) string { return t.In(location(config)).Format("15:04") }
	// In bedtime a distraction time opens nothing, so a door there is an
	// ordinary window at the bedtime rate.
	if end, in := policy.DistractionTimeEnds(config, now); in && !policy.InBedtime(config, now) {
		door.InDistractionTime, door.UseMinutes, door.WindowEnd = true, 0, end.UTC()
		if !end.After(door.OpensAt) {
			return Door{}, Unavailable("Today's distraction time ends at " + clock(end) + ", before a door would open.")
		}
		return door, nil
	}
	door.WindowEnd = door.OpensAt.Add(time.Duration(settings.WindowMinutes) * time.Minute)
	// A window running into a distraction time stops at its start; from
	// then on the distraction time's own rule holds.
	if next, ok := policy.NextDistractionTime(config, now); ok && next.Before(door.WindowEnd) {
		if !next.After(door.OpensAt) {
			return Door{}, Unavailable("Distraction time starts at " + clock(next) + ", before a door would open.")
		}
		door.WindowEnd = next.UTC()
	}
	return door, nil
}
