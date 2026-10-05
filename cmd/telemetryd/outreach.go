package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/outreach"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/silence"
	"github.com/rimexe0/telemetry/internal/store"
)

// morningAttempts is how often a morning message that failed to send is
// tried again.
const morningAttempts = 3

// runOutreach writes to the owner without being asked, through the
// configured commands: a morning message when the records show they woke,
// and a check-in after a long stretch outside the day's tasks. Every message
// and every decision to stay quiet is logged, so the assistant can see later
// what was said in its name. With no send command it does nothing; with no
// compose command only the morning message is sent, as plain facts.
func runOutreach(ctx context.Context, repository store.Repository, compose, send []string) {
	if len(send) == 0 {
		return
	}
	attempts := map[string]int{}
	check := func() {
		now := time.Now().UTC()
		loadCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		config, location, err := outreachConfig(loadCtx, repository)
		if err != nil {
			log.Printf("outreach: %v", err)
			return
		}
		logged, err := repository.ListOutreach(loadCtx, now.Add(-36*time.Hour))
		if err != nil {
			log.Printf("outreach: %v", err)
			return
		}
		var delivered, all []outreach.Sent
		for _, entry := range logged {
			all = append(all, outreach.Sent{Kind: entry.Kind, At: entry.CreatedAt, Delivered: entry.Delivered})
			if entry.Delivered {
				delivered = append(delivered, outreach.Sent{Kind: entry.Kind, At: entry.CreatedAt})
			}
		}
		notifyCollection(ctx, repository, send, logged, location)
		wake, err := store.LoadWakeTime(loadCtx, repository, now)
		if err != nil {
			log.Printf("outreach: %v", err)
			return
		}
		today := now.In(location).Format("2006-01-02")
		if outreach.MorningDue(now, wake, delivered, location) && attempts[today] < morningAttempts {
			attempts[today]++
			facts, err := morningFacts(loadCtx, repository, config, location, *wake, now)
			if err != nil {
				log.Printf("outreach: %v", err)
				return
			}
			message := facts
			if len(compose) > 0 {
				composeCtx, cancelCompose := context.WithTimeout(ctx, 3*time.Minute)
				answer, err := outreach.Compose(composeCtx, compose, outreach.MorningPrompt(facts))
				cancelCompose()
				// The plain facts go out when the assistant cannot word them.
				if err != nil {
					log.Printf("outreach: %v", err)
				} else if composed := outreach.Message(answer); composed != "" {
					message = composed
				}
			}
			deliver(ctx, repository, send, outreach.KindMorning, message, facts)
			return
		}
		if len(compose) == 0 {
			return
		}
		stretch, facts, err := offPlan(loadCtx, repository, config, location, now)
		if err != nil {
			log.Printf("outreach: %v", err)
			return
		}
		if !outreach.OffPlanDue(now, stretch, all) {
			return
		}
		// The last check-in (the log is newest first), so the next one does
		// not say the same.
		for _, entry := range logged {
			if entry.Kind == outreach.KindOffPlan && entry.Delivered && entry.Message != "" {
				facts += fmt.Sprintf("\nTheir last check-in, at %s, said: %q", entry.CreatedAt.In(location).Format("Mon 15:04"), entry.Message)
				break
			}
		}
		composeCtx, cancelCompose := context.WithTimeout(ctx, 3*time.Minute)
		answer, err := outreach.Compose(composeCtx, compose, outreach.OffPlanPrompt(facts))
		cancelCompose()
		if err != nil {
			log.Printf("outreach: %v", err)
			answer = "NOTHING"
		}
		deliver(ctx, repository, send, outreach.KindOffPlan, outreach.Message(answer), facts)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		check()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// notifyCollection tells the owner about a collection problem that stops
// tracking or blocking once it has lasted, once per problem: see
// outreach.Problems and outreach.CollectionDue. A machine going quiet is not
// told, since it may be asleep; the phone going quiet is told only while the
// owner is using the Mac.
func notifyCollection(ctx context.Context, repository store.Repository, send []string, logged []store.Outreach, location *time.Location) {
	now := time.Now().UTC()
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	incidents, err := repository.ListCollectionIncidents(listCtx, now, 100)
	if err != nil {
		log.Printf("outreach: %v", err)
		return
	}
	told := []outreach.Told{}
	for _, entry := range logged {
		if entry.Kind == outreach.KindCollection && entry.Delivered {
			told = append(told, outreach.Told{Facts: entry.Facts, At: entry.CreatedAt})
		}
	}
	for _, incident := range incidents {
		if incident.EndedAt != nil || !outreach.CollectionDue(incident.ID, incident.Device, incident.Kind, incident.StartedAt, now, told) {
			continue
		}
		if incident.Kind == silence.KindSilent {
			if incident.Device != silence.DevicePhone {
				continue
			}
			active, err := macActive(listCtx, repository, location, incident.StartedAt, now)
			if err != nil {
				log.Printf("outreach: %v", err)
				continue
			}
			if active < outreach.PhoneSilentWhileActive {
				continue
			}
		}
		device := incident.Device
		if strings.HasPrefix(device, "mac-") {
			device = "the Mac"
		}
		marker := outreach.CollectionMarker(incident.ID, incident.Device, incident.Kind)
		message := outreach.Problems[incident.Kind].Message(device, incident.StartedAt.In(location).Format("Mon 15:04"), incident.Detail)
		deliver(ctx, repository, send, outreach.KindCollection, message, marker)
		told = append(told, outreach.Told{Facts: marker, At: now})
	}
}

// macActive is the owner's active time on the Mac in [from, to).
func macActive(ctx context.Context, repository store.Repository, location *time.Location, from, to time.Time) (time.Duration, error) {
	planDate, _, _ := localDay(to, location)
	input, _, err := store.LoadDayInput(ctx, repository, planDate, from, to, false)
	if err != nil {
		return 0, err
	}
	var active float64
	for _, chunk := range activity.BuildDay(input).Chunks {
		if chunk.Device == activity.DeviceMac {
			active += chunk.ActiveMinutes
		}
	}
	return time.Duration(active * float64(time.Minute)), nil
}

// deliver sends a message and logs it; an empty message is logged as a
// decision to stay quiet.
func deliver(ctx context.Context, repository store.Repository, send []string, kind, message, facts string) {
	entry := store.Outreach{Kind: kind, Message: message, Facts: facts}
	if message != "" {
		sendCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err := outreach.Send(sendCtx, send, message)
		cancel()
		entry.Delivered = err == nil
		if err != nil {
			entry.Error = err.Error()
		}
	}
	saveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := repository.AddOutreach(saveCtx, entry); err != nil {
		log.Printf("outreach: %v", err)
	}
	log.Printf("outreach: %s delivered=%v quiet=%v %s", kind, entry.Delivered, message == "", entry.Error)
}

func outreachConfig(ctx context.Context, repository store.Repository) (policy.Config, *time.Location, error) {
	config := policy.Config{}
	stored, err := repository.LatestPolicy(ctx)
	if err != nil {
		return config, nil, err
	}
	if stored != nil {
		config = stored.Config
	}
	if err := config.Normalize(); err != nil {
		return config, nil, err
	}
	location, err := time.LoadLocation(config.Timezone)
	return config, location, err
}

// localDay returns the plan date of the local day at holds, and that day's
// start and end.
func localDay(at time.Time, location *time.Location) (time.Time, time.Time, time.Time) {
	local := at.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC), start, start.AddDate(0, 0, 1)
}

func minutes(value float64) string {
	total := int(value + 0.5)
	if total >= 60 {
		return fmt.Sprintf("%d h %d min", total/60, total%60)
	}
	return fmt.Sprintf("%d min", total)
}

// taskLines lists the open tasks with their next steps.
func taskLines(tasks []activity.TaskInfo) []string {
	lines := []string{}
	for _, task := range tasks {
		line := "- " + task.Title
		if task.NextAction != "" {
			line += " (next step: " + task.NextAction + ")"
		}
		lines = append(lines, line)
	}
	return lines
}

// morningFacts is what the records say at the start of the owner's day.
func morningFacts(ctx context.Context, repository store.Repository, config policy.Config, location *time.Location, wake, now time.Time) (string, error) {
	planDate, dayStart, _ := localDay(now, location)
	lines := []string{
		"Today is " + now.In(location).Format("Monday 2 January") + ". First use of a device after sleep: " + wake.In(location).Format("15:04") + ".",
	}
	input, _, err := store.LoadDayInput(ctx, repository, planDate.AddDate(0, 0, -1), dayStart.AddDate(0, 0, -1), dayStart, false)
	if err != nil {
		return "", err
	}
	yesterday := activity.BuildDay(input)
	var active float64
	for _, chunk := range yesterday.Chunks {
		active += chunk.ActiveMinutes
	}
	line := "Yesterday: " + minutes(active) + " at devices, " + minutes(yesterday.DistractionMinutes) + " on distractions."
	for _, task := range yesterday.Tasks {
		if task.Minutes >= 1 || task.AgentMinutes >= 1 {
			line += fmt.Sprintf(" %s: %s of their own time, its agent ran %s.", task.Title, minutes(task.Minutes), minutes(task.AgentMinutes))
		}
	}
	if len(yesterday.NoData) > 0 {
		line += " Some of yesterday has no data, so these undercount."
	}
	lines = append(lines, line)
	recent, _, err := store.LoadDayInput(ctx, repository, planDate, now.Add(-12*time.Hour), now, false)
	if err != nil {
		return "", err
	}
	for _, turn := range activity.Track(recent).YourTurn {
		lines = append(lines, "Waiting for them: "+turn.Task+" (its agent stopped "+turn.Since.In(location).Format("Mon 15:04")+").")
	}
	followUps, err := repository.ListFollowUps(ctx, now)
	if err != nil {
		return "", err
	}
	for _, followUp := range followUps {
		if followUp.State == "open" && !followUp.DueAt.After(now.Add(12*time.Hour)) {
			lines = append(lines, "Follow-up: "+followUp.Title+" (due "+followUp.DueAt.In(location).Format("Mon 15:04")+").")
		}
	}
	plan, err := repository.LatestPlan(ctx, planDate)
	if err != nil {
		return "", err
	}
	switch {
	case plan == nil && config.PlanGate.FromWake:
		lines = append(lines, "Today has no plan yet. Everything except the essentials is locked until the day is planned.")
	case plan == nil:
		lines = append(lines, "Today has no plan yet.")
	default:
		lines = append(lines, "Today already has a plan:")
		for _, block := range plan.Blocks {
			lines = append(lines, "- "+block.StartTime.In(location).Format("15:04")+" "+block.Title)
		}
	}
	if len(recent.Tasks) > 0 {
		lines = append(lines, "Open tasks:")
		lines = append(lines, taskLines(recent.Tasks)...)
	}
	return strings.Join(lines, "\n"), nil
}

// offPlan measures the last stretch of the owner's day and words it.
func offPlan(ctx context.Context, repository store.Repository, config policy.Config, location *time.Location, now time.Time) (outreach.Stretch, string, error) {
	planDate, _, _ := localDay(now, location)
	from := now.Add(-outreach.OffPlanWindow)
	input, _, err := store.LoadDayInput(ctx, repository, planDate, from, now, false)
	if err != nil {
		return outreach.Stretch{}, "", err
	}
	day, tracking := activity.BuildDay(input), activity.Track(input)
	stretch := outreach.Stretch{Excused: tracking.Excused, AgentRunning: tracking.AgentRunning, OpenTasks: len(input.Tasks), Away: true,
		Leisure: policy.DistractionTime(config, now)}
	for _, message := range input.Messages {
		stretch.Coding = stretch.Coding || (!message.At.Before(from) && message.At.Before(now))
	}
	on, active := map[string]float64{}, map[string]float64{}
	for _, chunk := range day.Chunks {
		// Two devices in use at once are one stretch of time, not two.
		active[chunk.Device] += chunk.ActiveMinutes
		if used := time.Duration(active[chunk.Device] * float64(time.Minute)); used > stretch.Active {
			stretch.Active = used
		}
		stretch.OnTasks += time.Duration(chunk.TaskMinutes * float64(time.Minute))
		// Away means on no device, not only away from the Mac.
		if now.Sub(chunk.End) <= 5*time.Minute {
			stretch.Away = false
		}
		for _, item := range chunk.Items {
			name := item.Name
			if item.Detail != "" {
				name += " · " + item.Detail
			}
			if item.Label == policy.LabelDistraction {
				name += " (distraction)"
				stretch.Distracted += time.Duration(item.Minutes * float64(time.Minute))
			}
			on[chunk.Device+": "+name] += item.Minutes
		}
	}
	block := ""
	for _, planned := range input.Blocks {
		if !now.Before(planned.Start) && now.Before(planned.End) {
			block = fmt.Sprintf("Planned for now: %q until %s.", planned.Title, planned.End.In(location).Format("Mon 15:04"))
			if planned.TaskID == nil {
				block += " It is not tied to a task."
				// A block with no task that leaves distractions open is free time.
				stretch.Leisure = stretch.Leisure || !planned.Closes
			}
		}
	}
	stretch.Mac = time.Duration(active[activity.DeviceMac] * float64(time.Minute))
	names := make([]string, 0, len(on))
	for name := range on {
		names = append(names, name)
		if largest := time.Duration(on[name] * float64(time.Minute)); !strings.HasSuffix(name, "(distraction)") && largest > stretch.Largest {
			stretch.Largest = largest
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return on[names[i]] > on[names[j]] || (on[names[i]] == on[names[j]] && names[i] < names[j])
	})
	lines := []string{fmt.Sprintf("Now %s. In the last %d minutes: %s on the Mac, %s on the phone, %s of it on today's tasks.", now.In(location).Format("15:04"),
		int(outreach.OffPlanWindow.Minutes()), minutes(active[activity.DeviceMac]), minutes(active[activity.DevicePhone]), minutes(stretch.OnTasks.Minutes()))}
	for i, name := range names {
		if i == 4 {
			break
		}
		lines = append(lines, "- "+name+": "+minutes(on[name]))
	}
	plan, err := repository.LatestPlan(ctx, planDate)
	if err != nil {
		return outreach.Stretch{}, "", err
	}
	switch {
	case plan == nil:
		lines = append(lines, "Today has no plan yet.")
	case block != "":
		lines = append(lines, block)
	default:
		lines = append(lines, "Nothing is planned for this hour.")
	}
	for _, turn := range tracking.YourTurn {
		lines = append(lines, "Waiting for them: "+turn.Task+" (its agent stopped "+turn.Since.In(location).Format("15:04")+").")
	}
	lines = append(lines, "Open tasks:")
	lines = append(lines, taskLines(input.Tasks)...)
	return stretch, strings.Join(lines, "\n"), nil
}

// previewOutreach prints what both messages would be written from right
// now, and their wording when a compose command is given. It sends and
// stores nothing.
func previewOutreach(ctx context.Context, repository store.Repository, compose []string) error {
	now := time.Now().UTC()
	config, location, err := outreachConfig(ctx, repository)
	if err != nil {
		return err
	}
	wake, err := store.LoadWakeTime(ctx, repository, now)
	if err != nil {
		return err
	}
	if wake == nil {
		fmt.Println("Woke: not shown by the records.")
		wake = &now
	} else {
		fmt.Println("Woke:", wake.In(location).Format("2006-01-02 15:04"))
	}
	morning, err := morningFacts(ctx, repository, config, location, *wake, now)
	if err != nil {
		return err
	}
	stretch, off, err := offPlan(ctx, repository, config, location, now)
	if err != nil {
		return err
	}
	fmt.Printf("\nMorning facts:\n%s\n\nOff-plan stretch: %+v, due without the cap: %v\nOff-plan facts:\n%s\n", morning, stretch, outreach.OffPlanDue(now, stretch, nil), off)
	if len(compose) == 0 {
		return nil
	}
	for name, prompt := range map[string]string{"Morning": outreach.MorningPrompt(morning), "Off-plan": outreach.OffPlanPrompt(off)} {
		answer, err := outreach.Compose(ctx, compose, prompt)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s message:\n%s\n", name, answer)
	}
	return nil
}
