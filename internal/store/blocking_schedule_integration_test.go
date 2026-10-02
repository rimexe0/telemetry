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
