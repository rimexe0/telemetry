package store

import (
	"testing"
	"time"
)

func TestLoopholeRemindersComeHourlyUntilTheMerge(t *testing.T) {
	reported := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	merge := reported.Add(LoopholeMergeAfter)
	c := LoopholeCase{ReportedAt: &reported, Message: "Bedtime was switched off at 23:59.", PRURL: "https://github.com/x/y/pull/1", MergeAfter: &merge}
	if !c.ReminderDue(reported) {
		t.Fatal("told once at the report")
	}
	sent := 0
	for at := reported; at.Before(merge.Add(time.Hour)); at = at.Add(5 * time.Minute) {
		if c.ReminderDue(at) {
			sent++
			c.Reminders++
			when := at
			c.RemindedAt = &when
		}
	}
	if sent != LoopholeReminders {
		t.Fatalf("%d messages, want %d", sent, LoopholeReminders)
	}
	answered := reported.Add(time.Hour)
	c = LoopholeCase{ReportedAt: &reported, Message: "x", PRURL: "https://github.com/x/y/pull/1", MergeAfter: &merge, AnsweredAt: &answered}
	if c.ReminderDue(reported.Add(2 * time.Hour)) {
		t.Fatal("reminded after an answer")
	}
	once := LoopholeCase{ReportedAt: &reported, Message: "Nothing to fix."}
	if !once.ReminderDue(reported) {
		t.Fatal("a report without a fix is told once")
	}
	once.Reminders = 1
	if once.ReminderDue(reported.Add(2 * time.Hour)) {
		t.Fatal("a report without a fix is told only once")
	}
}
