package policy

import (
	"fmt"
	"slices"
	"time"
)

// SettingsLock keeps blocking from being loosened during the owner's chosen
// times, by the assistant or the panel alike (owner request 2026-10-30). The
// only way past it is a PIN someone else holds, entered during the PIN
// times, which lifts the lock for UnlockMinutes.
type SettingsLock struct {
	Enabled bool `json:"enabled"`
	// Windows are when the lock holds; none means at all times.
	Windows []Window `json:"windows,omitempty"`
	// PinWindows are when the PIN is accepted; none means at any time.
	PinWindows    []Window `json:"pin_windows,omitempty"`
	UnlockMinutes int      `json:"unlock_minutes,omitempty"`
}

func (l SettingsLock) Normalize() error {
	for _, window := range append(append([]Window{}, l.Windows...), l.PinWindows...) {
		if !clockPattern.MatchString(window.Start) || !clockPattern.MatchString(window.End) || window.Start == window.End {
			return fmt.Errorf("%w: a lock time needs a start and a different end as HH:MM", ErrInvalidConfig)
		}
	}
	if len(l.Windows) > 20 || len(l.PinWindows) > 20 {
		return fmt.Errorf("%w: at most 20 lock times are supported", ErrInvalidConfig)
	}
	if l.Enabled && (l.UnlockMinutes < 1 || l.UnlockMinutes > 240) {
		return fmt.Errorf("%w: settings_lock.unlock_minutes must be between 1 and 240", ErrInvalidConfig)
	}
	return nil
}

// covers reports whether every stretch of outer's windows also lies in
// inner's: the same windows, or more of them.
func coversAll(inner, outer []Window) bool {
	for _, window := range outer {
		if !slices.Contains(inner, window) {
			return false
		}
	}
	return true
}

// Looser reports whether next asks less of the owner: off, fewer lock
// times, more PIN times, or a longer unlock. Changing the times in any
// other way counts as looser too.
func (l SettingsLock) Looser(next SettingsLock) bool {
	if !l.Enabled {
		return false
	}
	if !next.Enabled {
		return true
	}
	lockAlways, nextAlways := len(l.Windows) == 0, len(next.Windows) == 0
	fewerLockTimes := !nextAlways && (lockAlways || !coversAll(next.Windows, l.Windows))
	pinAlways, nextPinAlways := len(l.PinWindows) == 0, len(next.PinWindows) == 0
	morePinTimes := !pinAlways && (nextPinAlways || !coversAll(l.PinWindows, next.PinWindows))
	return fewerLockTimes || morePinTimes || next.UnlockMinutes > l.UnlockMinutes
}

// SettingsLockAt is the settings lock in force at an instant.
func (c Config) SettingsLockAt(at time.Time) SettingsLock {
	return ruleAt(c.Timezone, c.SettingsLock, c.SettingsLockNext, c.SettingsLockNextFrom, at)
}

// WithSettingsLock sets the lock as of now; a looser one waits for the next
// local day.
func (c Config) WithSettingsLock(next SettingsLock, now time.Time) Config {
	c.SettingsLock, c.SettingsLockNext, c.SettingsLockNextFrom = withRule(c.Timezone, c.SettingsLock, c.SettingsLockNext, c.SettingsLockNextFrom, next, SettingsLock.Looser, now)
	return c
}

// inWindows reports whether at falls in one of windows and when that
// stretch ends.
func inWindows(windows []Window, location *time.Location, at time.Time) (time.Time, bool) {
	local := at.In(location)
	for offset := -1; offset <= 0; offset++ {
		day := local.AddDate(0, 0, offset).Format("2006-01-02")
		for _, window := range windows {
			start, err1 := time.ParseInLocation("2006-01-02 15:04", day+" "+window.Start, location)
			end, err2 := time.ParseInLocation("2006-01-02 15:04", day+" "+window.End, location)
			if err1 != nil || err2 != nil {
				continue
			}
			if !end.After(start) {
				end = end.AddDate(0, 0, 1)
			}
			if !at.Before(start) && at.Before(end) {
				return end, true
			}
		}
	}
	return time.Time{}, false
}

// Locked reports whether the settings lock holds at an instant, and until
// when when that is known (zero for a lock at all times).
func (c Config) Locked(at time.Time) (bool, time.Time) {
	lock := c.SettingsLockAt(at)
	if !lock.Enabled {
		return false, time.Time{}
	}
	if len(lock.Windows) == 0 {
		return true, time.Time{}
	}
	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		location = time.UTC
	}
	end, in := inWindows(lock.Windows, location, at)
	return in, end
}

// PinOpen reports whether the PIN is accepted at an instant.
func (c Config) PinOpen(at time.Time) bool {
	lock := c.SettingsLockAt(at)
	if len(lock.PinWindows) == 0 {
		return true
	}
	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		location = time.UTC
	}
	_, in := inWindows(lock.PinWindows, location, at)
	return in
}

// ModeBedtime locks the owner out of their screens during bed hours without
// closing anything, so agents keep running (owner request 2026-10-30).
const ModeBedtime = "bedtime"

// Bedtime is the owner's nightly bed hours.
type Bedtime struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

func (b Bedtime) Normalize() error {
	if b.Enabled && (!clockPattern.MatchString(b.Start) || !clockPattern.MatchString(b.End) || b.Start == b.End) {
		return fmt.Errorf("%w: bedtime needs a start and a different end as HH:MM", ErrInvalidConfig)
	}
	return nil
}

// Looser reports whether next asks less: off, or hours that leave out any
// minute of the current ones.
func (b Bedtime) Looser(next Bedtime) bool {
	if !b.Enabled {
		return false
	}
	if !next.Enabled {
		return true
	}
	covered := minutesOf(next)
	for minute, in := range minutesOf(b) {
		if in && !covered[minute] {
			return true
		}
	}
	return false
}

// minutesOf marks each minute of the day bedtime covers.
func minutesOf(b Bedtime) [1440]bool {
	var minutes [1440]bool
	start, err1 := time.Parse("15:04", b.Start)
	end, err2 := time.Parse("15:04", b.End)
	if err1 != nil || err2 != nil {
		return minutes
	}
	from, to := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
	for minute := from; minute != to; minute = (minute + 1) % 1440 {
		minutes[minute] = true
	}
	return minutes
}

// BedtimeAt is the bedtime in force at an instant.
func (c Config) BedtimeAt(at time.Time) Bedtime {
	return ruleAt(c.Timezone, c.Bedtime, c.BedtimeNext, c.BedtimeNextFrom, at)
}

// WithBedtime sets bedtime as of now; a looser one waits for the next day.
func (c Config) WithBedtime(next Bedtime, now time.Time) Config {
	c.Bedtime, c.BedtimeNext, c.BedtimeNextFrom = withRule(c.Timezone, c.Bedtime, c.BedtimeNext, c.BedtimeNextFrom, next, Bedtime.Looser, now)
	return c
}

func bedtimeEnd(rule Bedtime, location *time.Location, at time.Time) (time.Time, bool) {
	if !rule.Enabled {
		return time.Time{}, false
	}
	return inWindows([]Window{{Start: rule.Start, End: rule.End}}, location, at)
}
