package activity

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildDayCompressesWithoutDroppingTime(t *testing.T) {
	at := func(hour, minute, second int) time.Time {
		return time.Date(2026, 10, 1, hour, minute, second, 0, time.UTC)
	}
	labels := []Label{{Kind: LabelKindApp, Match: "com.twitter.android", Label: Distraction}, {Kind: LabelKindSite, Match: "youtube.com", Label: Distraction}}
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		// Phone: X with a 30-second glance at WhatsApp inside it, then a long pause, then Anki.
		{Device: DevicePhone, App: "com.twitter.android", Start: at(9, 0, 0), End: at(9, 10, 0)},
		{Device: DevicePhone, App: "com.whatsapp", Start: at(9, 10, 0), End: at(9, 10, 30)},
		{Device: DevicePhone, App: "com.twitter.android", Start: at(9, 10, 30), End: at(9, 20, 0)},
		{Device: DevicePhone, App: "com.ichi2.anki", Start: at(10, 0, 0), End: at(10, 15, 0)},
		// Mac: code, then YouTube long enough to be its own chunk, then code.
		{Device: DeviceMac, App: "Code", Title: "main.go", Start: at(9, 0, 0), End: at(9, 30, 0)},
		{Device: DeviceMac, App: "Helium", Domain: "www.youtube.com", Title: "lofi", Start: at(9, 30, 0), End: at(9, 45, 0)},
		{Device: DeviceMac, App: "Code", Title: "main.go", Start: at(9, 45, 0), End: at(10, 0, 0)},
	}}
	blocks := []PlannedBlock{
		{ID: 1, Title: "Telemetry", Start: at(9, 0, 0), End: at(10, 0, 0), State: "planned"},
		{ID: 2, Title: "Later", Start: at(12, 0, 0), End: at(13, 0, 0), State: "planned"},
	}
	day := BuildDay(DayInput{Blocks: blocks, Result: result, Labels: labels, From: at(0, 0, 0), To: at(10, 30, 0), WithSegments: true})

	var phone, mac []Chunk
	var active float64
	segments := 0
	for _, chunk := range day.Chunks {
		active += chunk.ActiveMinutes
		segments += len(chunk.Segments)
		if chunk.Device == DevicePhone {
			phone = append(phone, chunk)
		} else {
			mac = append(mac, chunk)
		}
	}
	if active != 95 || segments != len(result.Segments) {
		t.Fatalf("chunks must keep all time and every segment: %v minutes, %d segments", active, segments)
	}
	if len(phone) != 2 || phone[0].Kind != KindDistraction || phone[0].ActiveMinutes != 20 || phone[0].DistractionMinutes != 19.5 ||
		len(phone[0].Items) != 2 || phone[0].Items[0].Visits != 2 || phone[1].Kind != KindOther {
		t.Fatalf("phone chunks = %+v", phone)
	}
	if len(mac) != 3 || mac[0].Kind != KindOther || mac[1].Kind != KindDistraction || mac[1].Items[0].Name != "www.youtube.com" || mac[2].Kind != KindOther {
		t.Fatalf("mac chunks = %+v", mac)
	}
	// 09:00-10:00: the Mac was in Code for 45 minutes, but the phone showed X
	// for 19.5 of them, so 25.5 count as in plan.
	if first := day.Blocks[0]; first.Status != "past" || first.InPlanMinutes != 25.5 || first.DistractionMinutes != 34.5 {
		t.Fatalf("first block = %+v", first)
	}
	if second := day.Blocks[1]; second.Status != "upcoming" || second.InPlanMinutes != 0 {
		t.Fatalf("second block = %+v", second)
	}
	if day.PlannedMinutes != 120 || day.InPlanMinutes != 25.5 || day.DistractionMinutes != 34.5 || day.Distraction[0].Minutes != 15 || day.Distraction[1].Minutes != 19.5 {
		t.Fatalf("totals = %+v", day)
	}
	if plain := BuildDay(DayInput{Result: result, Labels: labels, From: at(0, 0, 0), To: at(10, 30, 0)}); plain.Chunks[0].Segments != nil || len(plain.Blocks) != 0 {
		t.Fatalf("without segments = %+v", plain.Chunks[0])
	}
	// A focus block with no task only closes distractions: it plans nothing and counts nothing.
	focus := BuildDay(DayInput{Blocks: []PlannedBlock{{ID: 9, Title: "Rest", Start: at(9, 0, 0), End: at(10, 0, 0), State: "planned", Closes: true}},
		Result: result, Labels: labels, From: at(0, 0, 0), To: at(10, 30, 0)})
	if focus.PlannedMinutes != 0 || focus.InPlanMinutes != 0 || focus.Blocks[0].InPlanMinutes != 0 || focus.Blocks[0].DistractionMinutes != 34.5 {
		t.Fatalf("focus-only block = %+v", focus)
	}
}

func TestT3TimeIsNamedByTheNearestMessage(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	turn := func(thread, requested string) Record {
		return Record{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"` + thread + `","requested_at":"` + requested + `"}`)}
	}
	messages := ThreadMessages([]Record{
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"p","title":"Telemetry"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","project_id":"p","title":"Fix collector"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"b","project_id":"p","title":"Phone app"}`)},
		turn("a", "2026-10-01T09:05:00Z"), turn("b", "2026-10-01T09:40:00Z"),
	})
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(9, 0), End: at(9, 10)},
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(9, 12), End: at(9, 20)},
		{Device: DevicePhone, App: "com.t3tools.t3code", Start: at(9, 35), End: at(9, 45)},
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(12, 0), End: at(12, 10)},
		{Device: DeviceMac, App: "Code", Start: at(12, 10), End: at(12, 12)},
	}}
	day := BuildDay(DayInput{Result: result, Messages: messages, From: at(0, 0), To: at(13, 0), WithSegments: true})
	details := map[string]float64{}
	for _, chunk := range day.Chunks {
		for _, item := range chunk.Items {
			details[item.Name+"|"+item.Detail] += item.Minutes
		}
	}
	// 09:00-09:20 on the Mac is near the 09:05 message; the phone's stretch
	// holds the 09:40 one; nothing was sent near 12:00, so that stays unnamed.
	if details["T3 Code (Nightly)|Telemetry · Fix collector"] != 18 || details["com.t3tools.t3code|Telemetry · Phone app"] != 10 ||
		details["T3 Code (Nightly)|"] != 10 || details["Code|"] != 2 {
		t.Fatalf("details = %+v", details)
	}
}

func TestTrackedBlocksCountOnlyTimeOnTheTask(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	task := int64(7)
	messages := ThreadMessages([]Record{
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"p","title":"Telemetry","workspace_root":"/Users/ri/code/personal/Telemetry"}`)},
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"q","title":"Other","workspace_root":"/Users/ri/code/other"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","project_id":"p","title":"Fix collector"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"b","project_id":"q","title":"Side quest"}`)},
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"a","requested_at":"2026-10-01T09:05:00Z"}`)},
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"b","requested_at":"2026-10-01T09:50:00Z"}`)},
	})
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(9, 0), End: at(9, 20)},             // the task's project
		{Device: DeviceMac, App: "Helium", Domain: "github.com", Start: at(9, 20), End: at(9, 30)}, // a task site
		{Device: DeviceMac, App: "Helium", Domain: "news.ycombinator.com", Start: at(9, 30), End: at(9, 40)},
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(9, 45), End: at(9, 55)},  // another project
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(10, 0), End: at(10, 10)}, // after the block
	}}
	blocks := []PlannedBlock{{ID: 1, Title: "Telemetry", TaskID: &task, Start: at(9, 0), End: at(10, 0), State: "planned", Closes: true}}
	tasks := []TaskInfo{{ID: task, Title: "Telemetry", Rules: []TaskRule{{Signal: "path_prefix", Pattern: "/Users/ri/code/personal/Telemetry"}, {Signal: "domain", Pattern: "github.com"}}}}
	day := BuildDay(DayInput{Blocks: blocks, Result: result, Messages: messages, Tasks: tasks, From: at(0, 0), To: at(11, 0)})
	if block := day.Blocks[0]; !block.Tracked || block.InPlanMinutes != 30 {
		t.Fatalf("block = %+v", block)
	}
	kinds := []string{}
	for _, chunk := range day.Chunks {
		kinds = append(kinds, chunk.Kind)
	}
	// 09:00-09:30 on task, then other. T3 Code after the block has no message
	// near it, so nothing says which project it was and it stays "other".
	if len(day.Chunks) != 2 || day.Chunks[0].Kind != KindTask || day.Chunks[0].Task != "Telemetry" || day.Chunks[0].TaskMinutes != 30 ||
		day.Chunks[1].Kind != KindOther || day.Chunks[1].TaskMinutes != 0 {
		t.Fatalf("kinds = %v chunks = %+v", kinds, day.Chunks)
	}
	if len(day.Tasks) != 1 || !day.Tasks[0].Tracked || day.Tasks[0].Minutes != 30 || day.InPlanMinutes != 30 {
		t.Fatalf("tasks = %+v in plan %v", day.Tasks, day.InPlanMinutes)
	}
	// The same day without rules counts any undistracted time, and says so.
	untracked := BuildDay(DayInput{Blocks: blocks, Result: result, Messages: messages, From: at(0, 0), To: at(11, 0)})
	if block := untracked.Blocks[0]; block.Tracked || block.InPlanMinutes != 50 {
		t.Fatalf("untracked block = %+v", block)
	}
}

func TestTasksAreTrackedWithoutBlocksAndClaimsSettleWhatRulesCannot(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	album, slides := int64(3), int64(4)
	tasks := []TaskInfo{
		{ID: album, Title: "Dad's album", Rules: []TaskRule{{Signal: "app", Pattern: "T3 Code (Nightly)"}}},
		{ID: slides, Title: "Science fair presentation", Rules: []TaskRule{{Signal: "domain", Pattern: "docs.google.com"}}},
	}
	result := Result{LastRecord: map[string]time.Time{}, Segments: []Segment{
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(14, 0), End: at(14, 30)},
		{Device: DeviceMac, App: "Helium", Domain: "docs.google.com", Start: at(14, 30), End: at(14, 50)},
		{Device: DeviceMac, App: "Keynote", Start: at(14, 50), End: at(15, 10)},
		// Away for an hour, then Keynote again.
		{Device: DeviceMac, App: "Keynote", Start: at(16, 10), End: at(16, 20)},
	}}
	// No blocks at all: the tasks still get their time.
	day := BuildDay(DayInput{Result: result, Tasks: tasks, From: at(0, 0), To: at(17, 0)})
	if day.Tasks[0].Minutes != 30 || day.Tasks[1].Minutes != 20 || day.InPlanMinutes != 50 || day.PlannedMinutes != 0 {
		t.Fatalf("without claims = %+v, in plan %v", day.Tasks, day.InPlanMinutes)
	}
	// At 14:52 the owner says Keynote is the presentation. It holds while they
	// stay at the Mac, and not after they have been away.
	claimed := BuildDay(DayInput{Result: result, Tasks: tasks, Claims: []Claim{{TaskID: &slides, Context: "Keynote", Start: at(14, 52)}}, From: at(0, 0), To: at(17, 0)})
	if claimed.Tasks[1].Minutes != 38 || claimed.Tasks[0].Minutes != 30 {
		t.Fatalf("a claim takes effect in the middle of a window: %+v", claimed.Tasks)
	}
	earlier := BuildDay(DayInput{Result: result, Tasks: tasks, Claims: []Claim{{TaskID: &slides, Context: "Keynote", Start: at(14, 50)}}, From: at(0, 0), To: at(17, 0)})
	if earlier.Tasks[1].Minutes != 40 || earlier.Tasks[0].Minutes != 30 {
		t.Fatalf("claimed = %+v", earlier.Tasks)
	}
	notWork := BuildDay(DayInput{Result: result, Tasks: tasks, Claims: []Claim{{Context: "Keynote", Start: at(13, 59)}}, From: at(0, 0), To: at(17, 0)})
	// "Not on the plan" settles only what no rule decides: recognised time keeps its task.
	if notWork.Tasks[0].Minutes != 30 || notWork.Tasks[1].Minutes != 20 {
		t.Fatalf("a claim must not override the rules: %+v", notWork.Tasks)
	}
	// Nor does a claim for one task swallow time the rules give to another.
	// A claim is about the one thing that was on screen, never about everything else.
	swallow := BuildDay(DayInput{Result: result, Tasks: tasks, Claims: []Claim{{TaskID: &album, Context: "Finder", Start: at(13, 59)}}, From: at(0, 0), To: at(17, 0)})
	if swallow.Tasks[0].Minutes != 30 || swallow.Tasks[1].Minutes != 20 {
		t.Fatalf("one answer must not swallow other things: %+v", swallow.Tasks)
	}
}

func TestTrackNamesWhatIsOnTheMac(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	album, slides := int64(3), int64(4)
	tasks := []TaskInfo{
		{ID: album, Title: "Dad's album", Rules: []TaskRule{{Signal: "path_prefix", Pattern: "/Users/ri/Downloads/WhatsApp Chat - Group"}}},
		{ID: slides, Title: "Science fair presentation"},
	}
	messages := ThreadMessages([]Record{
		{Source: "activitywatch:mac:aw-watcher-codex_derived_x", Kind: "activitywatch.event", Start: at(14, 5),
			Payload: []byte(`{"data":{"event_stream":"codex.turn","chat_title":"Create July session PDF","project_path":"/Users/ri/Downloads/WhatsApp Chat - Group"}}`)},
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"p","title":"Telemetry","workspace_root":"/Users/ri/code/personal/Telemetry"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","project_id":"p","title":"Clarify next steps"}`)},
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"a","requested_at":"2026-10-02T14:20:00Z"}`)},
	})
	mac := func(app string, from, to time.Time) Segment {
		return Segment{Device: DeviceMac, App: app, Start: from, End: to}
	}
	track := func(to time.Time, claims []Claim, segments ...Segment) Tracking {
		return Track(DayInput{Result: Result{Segments: segments}, Tasks: tasks, Messages: messages, Claims: claims, From: at(0, 0), To: to})
	}
	// The ChatGPT app is the album: its Codex thread works in the album's folder.
	if got := track(at(14, 10), nil, mac("ChatGPT", at(14, 0), at(14, 10))); got.State != KindTask || *got.TaskID != album ||
		got.Context != "ChatGPT · WhatsApp Chat - Group · Create July session PDF" || !got.Since.Equal(at(14, 0)) {
		t.Fatalf("codex thread = %+v", got)
	}
	// A T3 thread in another project is named as itself and is no task; nobody is asked.
	other := track(at(14, 25), nil, mac("T3 Code (Nightly)", at(14, 15), at(14, 25)))
	if other.State != KindOther || other.Context != "T3 Code (Nightly) · Telemetry · Clarify next steps" {
		t.Fatalf("off-plan project = %+v", other)
	}
	// The owner's word settles what no rule decides, here and now.
	if got := track(at(14, 3), []Claim{{TaskID: &slides, Context: "Keynote", Start: at(14, 2)}}, mac("Keynote", at(14, 0), at(14, 3))); got.State != KindTask || *got.TaskID != slides {
		t.Fatalf("after a claim it tracks that task: %+v", got)
	}
	if got := track(at(15, 0), nil, mac("Keynote", at(14, 0), at(14, 30))); got.State != "away" {
		t.Fatalf("nothing recent = %+v", got)
	}
}

func TestFocusSessionCountsOnlyTheTaskAndLeavesTheUnknownUnknown(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	album, slides := int64(3), int64(4)
	tasks := []TaskInfo{
		{ID: album, Title: "Dad's album", Rules: []TaskRule{{Signal: "app", Pattern: "T3 Code (Nightly)"}}},
		{ID: slides, Title: "Science fair presentation", NextAction: "Outline the three sections",
			Related: []string{"docs.google.com", "ChatGPT"}, Unrelated: []string{"trendyol.com"}},
	}
	labels := []Label{{Kind: LabelKindSite, Match: "youtube.com", Label: Distraction}}
	mac := func(app, domain string, from, to time.Time) Segment {
		return Segment{Device: DeviceMac, App: app, Domain: domain, Start: from, End: to}
	}
	segments := []Segment{
		mac("Helium", "docs.google.com", at(15, 0), at(15, 20)),   // learned: part of it
		mac("ChatGPT", "", at(15, 20), at(15, 30)),                // learned: part of it
		mac("T3 Code (Nightly)", "", at(15, 30), at(15, 40)),      // another planned task: off in a focus session
		mac("Helium", "trendyol.com", at(15, 40), at(15, 45)),     // learned: not part of it
		mac("Helium", "en.wikipedia.org", at(15, 45), at(15, 50)), // nothing known
		mac("Helium", "www.youtube.com", at(15, 50), at(15, 52)),
	}
	focus := []FocusSpan{{TaskID: slides, Start: at(15, 0), End: at(16, 0)}}
	day := BuildDay(DayInput{Result: Result{Segments: segments}, Labels: labels, Tasks: tasks, Focus: focus, From: at(0, 0), To: at(16, 0)})
	// 30 minutes on the presentation; T3 Code is the album's by rule but does not count while focusing on something else.
	if day.Tasks[1].Minutes != 30 || day.Tasks[0].Minutes != 0 {
		t.Fatalf("tasks = %+v", day.Tasks)
	}
	now := func(to time.Time, claims ...Claim) *FocusNow {
		var upTo []Segment
		for _, segment := range segments {
			if segment.Start.Before(to) {
				segment.End = earlier(segment.End, to)
				upTo = append(upTo, segment)
			}
		}
		return Track(DayInput{Result: Result{Segments: upTo}, Labels: labels, Tasks: tasks, Focus: []FocusSpan{{TaskID: slides, Start: at(15, 0), End: to}},
			Claims: claims, From: at(0, 0), To: to}).Focus
	}
	if got := now(at(15, 25)); got.On != FocusOn || got.Task != "Science fair presentation" || got.NextAction != "Outline the three sections" || got.OffSince != nil {
		t.Fatalf("on task = %+v", got)
	}
	if got := now(at(15, 44)); got.On != FocusOff || got.Context != "trendyol.com" || !got.OffSince.Equal(at(15, 30)) {
		t.Fatalf("left the task at 15:30: %+v", got)
	}
	unknown := now(at(15, 48))
	if unknown.On != FocusUnknown || unknown.Context != "en.wikipedia.org" || unknown.LearnContext != "en.wikipedia.org" {
		t.Fatalf("unknown = %+v", unknown)
	}
	// "Just this time": the owner's word for this sitting makes it part of the task without teaching anything.
	if got := now(at(15, 48), Claim{TaskID: &slides, Context: "en.wikipedia.org", Start: at(15, 46)}); got.On != FocusOn {
		t.Fatalf("just this time = %+v", got)
	}
	if got := now(at(15, 51)); got.On != FocusOff {
		t.Fatalf("a distraction is off: %+v", got)
	}
	if got := Track(DayInput{Result: Result{Segments: segments[:1]}, Tasks: tasks, Focus: []FocusSpan{{TaskID: slides, Start: at(15, 0), End: at(15, 40)}},
		From: at(0, 0), To: at(15, 40)}); got.State != "away" || got.Focus == nil || got.Focus.On != "away" {
		t.Fatalf("away during focus = %+v %+v", got, got.Focus)
	}
}

func TestLearnedMemoryAppliesAtProjectLevelAndCanOverrideARule(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	album := int64(3)
	messages := ThreadMessages([]Record{
		{Source: "t3:mac:env", Kind: "t3.project", Payload: []byte(`{"project_id":"p","title":"album-site","workspace_root":"/Users/ri/code/album-site"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"a","project_id":"p","title":"Fix the grid"}`)},
		{Source: "t3:mac:env", Kind: "t3.thread", Payload: []byte(`{"thread_id":"b","project_id":"p","title":"Add captions"}`)},
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"a","requested_at":"2026-10-02T14:05:00Z"}`)},
		{Source: "t3:mac:env", Kind: "t3.turn", Payload: []byte(`{"thread_id":"b","requested_at":"2026-10-02T14:35:00Z"}`)},
	})
	segments := []Segment{
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(14, 0), End: at(14, 10)},
		{Device: DeviceMac, App: "T3 Code (Nightly)", Start: at(14, 30), End: at(14, 40)},
		{Device: DeviceMac, App: "Finder", Start: at(14, 40), End: at(14, 45)},
	}
	// One "yes" about the project covers both of its threads.
	tasks := []TaskInfo{{ID: album, Title: "Dad's album", Related: []string{"T3 Code (Nightly) · album-site"}, Rules: []TaskRule{{Signal: "app", Pattern: "Finder"}}}}
	day := BuildDay(DayInput{Result: Result{Segments: segments}, Messages: messages, Tasks: tasks, From: at(0, 0), To: at(15, 0)})
	if day.Tasks[0].Minutes != 25 {
		t.Fatalf("project-level memory = %+v", day.Tasks)
	}
	// The owner later says Finder is not part of it: that beats the rule.
	tasks[0].Unrelated = []string{"Finder"}
	day = BuildDay(DayInput{Result: Result{Segments: segments}, Messages: messages, Tasks: tasks, From: at(0, 0), To: at(15, 0)})
	if day.Tasks[0].Minutes != 20 {
		t.Fatalf("an owner's no must override a rule: %+v", day.Tasks)
	}
}

func TestAgentTimeWaitingAndReturnsAreMeasuredPerTask(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 2, hour, minute, 0, 0, time.UTC) }
	album, slides := int64(3), int64(4)
	tasks := []TaskInfo{
		{ID: album, Title: "Dad's album", Rules: []TaskRule{{Signal: "path_prefix", Pattern: "/Users/ri/Downloads/WhatsApp Chat - Group"}}},
		{ID: slides, Title: "Science fair presentation", Related: []string{"docs.google.com"}},
	}
	codex := func(from, to time.Time) Record {
		return Record{Source: "activitywatch:mac:aw-watcher-codex_derived_x", Kind: "activitywatch.event", Start: from, End: &to,
			Payload: []byte(`{"data":{"event_stream":"codex.turn","thread_id":"t1","chat_title":"Create July session PDF","project_path":"/Users/ri/Downloads/WhatsApp Chat - Group"}}`)}
	}
	coding := []Record{codex(at(14, 5), at(14, 20)), codex(at(14, 50), at(15, 0))}
	mac := func(app, domain string, from, to time.Time) Segment {
		return Segment{Device: DeviceMac, App: app, Domain: domain, Start: from, End: to}
	}
	segments := []Segment{
		mac("ChatGPT", "", at(14, 0), at(14, 6)),                // writes the message that starts the first turn
		mac("Helium", "docs.google.com", at(14, 6), at(14, 40)), // fills the wait with the presentation
		mac("ChatGPT", "", at(14, 40), at(14, 51)),              // back on the album 20 minutes after the agent stopped
		mac("Helium", "docs.google.com", at(14, 51), at(15, 10)),
	}
	input := DayInput{Result: Result{Segments: segments}, Tasks: tasks, Messages: ThreadMessages(coding), Turns: AgentTurns(coding), From: at(0, 0), To: at(15, 10)}
	day := BuildDay(input)
	albumDay, slidesDay := day.Tasks[0], day.Tasks[1]
	// The agent ran 15 + 10 minutes. After its first stop at 14:20 the owner came back at 14:40: 20 minutes
	// waiting. After the second stop at 15:00 they have not come back: 10 more, and it is still their turn.
	if albumDay.Minutes != 17 || albumDay.AgentMinutes != 25 || albumDay.WaitingMinutes != 30 || albumDay.Returns != 1 ||
		albumDay.YourTurnSince == nil || !albumDay.YourTurnSince.Equal(at(15, 0)) || !albumDay.StartedAt.Equal(at(14, 0)) {
		t.Fatalf("album = %+v", albumDay)
	}
	if slidesDay.Minutes != 53 || slidesDay.AgentMinutes != 0 || slidesDay.Returns != 1 || slidesDay.YourTurnSince != nil {
		t.Fatalf("slides = %+v", slidesDay)
	}
	now := Track(input)
	if now.State != KindTask || *now.TaskID != slides || len(now.YourTurn) != 1 || now.YourTurn[0].TaskID != album || !now.YourTurn[0].Since.Equal(at(15, 0)) || now.AgentRunning {
		t.Fatalf("tracking = %+v", now)
	}
	// While the agent runs, nothing is the owner's turn and being elsewhere is filling a wait.
	input.To = at(14, 15)
	input.Result.Segments = []Segment{segments[0], mac("Helium", "docs.google.com", at(14, 6), at(14, 15))}
	input.Turns = []AgentTurn{{Tool: "codex", ThreadID: "codex\x00t1", Project: "WhatsApp Chat - Group", Root: "/Users/ri/Downloads/WhatsApp Chat - Group", Requested: at(14, 5)}}
	if during := Track(input); !during.AgentRunning || len(during.YourTurn) != 0 {
		t.Fatalf("during the run = %+v", during)
	}
	// A one-off the owner excused is marked, so nobody is told about it again.
	off := Track(DayInput{Result: Result{Segments: []Segment{mac("Obsidian", "", at(15, 0), at(15, 30))}}, Tasks: tasks,
		Claims: []Claim{{Context: "Obsidian", Start: at(15, 12)}}, From: at(0, 0), To: at(15, 30)})
	if off.State != KindOther || off.Context != "Obsidian" || off.LearnContext != "Obsidian" || !off.Excused {
		t.Fatalf("excused one-off = %+v", off)
	}
}

func TestThreadMessagesLeaveOutTurnsAnAgentStarted(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	source := "t3:mac-1:env"
	coding := []Record{
		{Source: source, Kind: "t3.project", Start: at, Payload: json.RawMessage(`{"project_id":"p1","title":"Telemetry","workspace_root":"/code/telemetry"}`)},
		{Source: source, Kind: "t3.thread", Start: at, Payload: json.RawMessage(`{"thread_id":"t1","project_id":"p1","title":"Fix sleep"}`)},
		{Source: source, Kind: "t3.turn", Start: at, Payload: json.RawMessage(`{"thread_id":"t1","state":"completed","requested_at":"2026-11-01T09:00:00Z","completed_at":"2026-11-01T09:05:00Z","requested_by":"user"}`)},
		{Source: source, Kind: "t3.turn", Start: at, Payload: json.RawMessage(`{"thread_id":"t1","state":"completed","requested_at":"2026-11-01T09:10:00Z","completed_at":"2026-11-01T09:15:00Z","requested_by":"agent"}`)},
		// Read from T3's database: the sender is unknown, so it counts.
		{Source: source, Kind: "t3.turn", Start: at, Payload: json.RawMessage(`{"thread_id":"t1","state":"completed","requested_at":"2026-11-01T09:20:00Z","completed_at":"2026-11-01T09:25:00Z"}`)},
	}
	messages := ThreadMessages(coding)
	if len(messages) != 2 || !messages[0].At.Equal(at) || messages[1].At.Minute() != 20 {
		t.Fatalf("messages = %+v", messages)
	}
	if turns := AgentTurns(coding); len(turns) != 3 || !turns[1].ByAgent {
		t.Fatalf("agent turns still count as agent work: %+v", turns)
	}
}
