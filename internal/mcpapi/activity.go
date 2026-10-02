package mcpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

const activityNotes = "Mac time excludes periods recorded as AFK (3 minutes without input, or locked); window time with no AFK record is kept and marked afk_unknown. " +
	"Browser time is split by the open tab's site. Phone apps are Android package names; a phone session ends at pause, the next app, screen off, or after one hour. " +
	"no_data lists periods with no records from a device: the Mac is asleep or its collector stopped; the phone collector reports every minute, so a phone gap usually means collection stopped. Missing data is never inactivity."

type ActivityRangeInput struct {
	Date string `json:"date,omitempty" jsonschema:"local calendar day YYYY-MM-DD; default today. Ignored when from is set"`
	From string `json:"from,omitempty" jsonschema:"inclusive RFC3339 start"`
	To   string `json:"to,omitempty" jsonschema:"exclusive RFC3339 end; default now"`
}

type ActivitySummaryInput struct {
	ActivityRangeInput
	Limit int `json:"limit,omitempty" jsonschema:"entries per ranked list from 1 through 100; default 15"`
}

type ActivitySummaryOutput struct {
	From     time.Time                `json:"from"`
	To       time.Time                `json:"to"`
	Timezone string                   `json:"timezone"`
	Devices  []activity.DeviceSummary `json:"devices"`
	NoData   []activity.Gap           `json:"no_data"`
	Notes    string                   `json:"notes"`
}

type ActivityTimelineInput struct {
	ActivityRangeInput
	MinSeconds int `json:"min_seconds,omitempty" jsonschema:"drop segments shorter than this; default 30"`
	Limit      int `json:"limit,omitempty" jsonschema:"maximum segments from 1 through 2000; default 400"`
}

type TimelineSegment struct {
	activity.Segment
	Minutes float64 `json:"minutes"`
}

type ActivityTimelineOutput struct {
	From      time.Time         `json:"from"`
	To        time.Time         `json:"to"`
	Timezone  string            `json:"timezone"`
	Segments  []TimelineSegment `json:"segments"`
	Omitted   int               `json:"omitted_short_segments"`
	Truncated bool              `json:"truncated"`
	NoData    []activity.Gap    `json:"no_data"`
	Notes     string            `json:"notes"`
}

type ActivityNowInput struct{}

type DeviceNow struct {
	Device             string           `json:"device"`
	LastRecord         *time.Time       `json:"last_record,omitempty"`
	MinutesSinceRecord *float64         `json:"minutes_since_last_record,omitempty"`
	Current            *TimelineSegment `json:"current,omitempty"`
	RecentApps         []activity.Usage `json:"last_hour_apps"`
	LastHourActive     float64          `json:"last_hour_active_minutes"`
}

type ActivityNowOutput struct {
	Now time.Time `json:"now"`
	// Coding is T3 Code threads with activity in the last hour, running now,
	// or waiting for the owner, on every machine that runs the collector.
	Coding []activity.CodingThread `json:"coding_threads"`
	// OpenIncidents are current collection problems; affected devices'
	// activity is incomplete.
	OpenIncidents []store.CollectionIncident `json:"open_collection_incidents"`
	Timezone      string                     `json:"timezone"`
	Devices       []DeviceNow                `json:"devices"`
	// WokeAt is the first use of a device after the last stretch of at least
	// three hours with none, local "YYYY-MM-DD HH:MM"; empty when the
	// records do not show one.
	WokeAt string `json:"woke_at,omitempty" jsonschema:"when the owner's day started, from the records; never ask them for it"`
	Notes  string `json:"notes"`
}

type CollectionIncidentsInput struct {
	Days int `json:"days,omitempty" jsonschema:"include incidents that ended within this many days, 1 through 90; default 7"`
}

type CollectionIncidentsOutput struct {
	Incidents []store.CollectionIncident `json:"incidents"`
	Notes     string                     `json:"notes"`
}

type ActivityLabelsInput struct{}

type ActivityLabelsOutput struct {
	Labels []store.ActivityLabel `json:"labels"`
	Notes  string                `json:"notes"`
}

const labelNotes = "Labels are the owner's own names for apps and sites; change them with change_activity_labels. " +
	"activity_summary totals time per label; a site label wins over the browser's app label."

type CodingThreadsInput struct {
	ActivityRangeInput
}

type CodingThreadsOutput struct {
	From    time.Time               `json:"from"`
	To      time.Time               `json:"to"`
	Threads []activity.CodingThread `json:"threads"`
	Notes   string                  `json:"notes"`
}

const codingNotes = "T3 Code threads from every machine running the Telemetry collector (each machine's T3 server records work done from any client: desktop, web or phone). " +
	"messages_sent counts the owner's messages in the range; agent_minutes is time the agent spent working; waiting_for_owner means an approval or answer is pending. Message contents are not collected."

func addActivityTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "coding_threads",
		Description: "T3 Code threads active in a day or range, or running or waiting for the owner now, newest first. " + codingNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CodingThreadsInput) (*mcp.CallToolResult, CodingThreadsOutput, error) {
		from, to, _, err := activityRange(ctx, repository, input.ActivityRangeInput, now())
		if err != nil {
			return nil, CodingThreadsOutput{}, err
		}
		records, err := repository.CodingRecords(ctx, from)
		if err != nil {
			return nil, CodingThreadsOutput{}, err
		}
		return nil, CodingThreadsOutput{From: from, To: to, Threads: activity.CodingThreads(records, from, to), Notes: codingNotes}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "activity_labels",
		Description: "The owner's current labels for apps and sites. " + labelNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ActivityLabelsInput) (*mcp.CallToolResult, ActivityLabelsOutput, error) {
		labels, err := repository.ListActivityLabels(ctx)
		if err != nil {
			return nil, ActivityLabelsOutput{}, err
		}
		return nil, ActivityLabelsOutput{Labels: labels, Notes: labelNotes}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "collection_incidents",
		Description: "Collection problems: a device or collector that went quiet unexpectedly, or a Mac collector that cannot see windows. " +
			"Open incidents (no ended_at) mean that device's activity is currently incomplete. Announced Mac sleep and stops are not incidents.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CollectionIncidentsInput) (*mcp.CallToolResult, CollectionIncidentsOutput, error) {
		days := input.Days
		if days == 0 {
			days = 7
		}
		if days < 1 || days > 90 {
			return nil, CollectionIncidentsOutput{}, errors.New("days must be between 1 and 90")
		}
		incidents, err := repository.ListCollectionIncidents(ctx, now().UTC().AddDate(0, 0, -days), 200)
		if err != nil {
			return nil, CollectionIncidentsOutput{}, err
		}
		return nil, CollectionIncidentsOutput{Incidents: incidents, Notes: "Incidents describe collection, never the owner's behaviour."}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "activity_now",
		Description: "What the owner is doing right now on the Mac and phone, how fresh each device's data is, and the last hour's apps. " + activityNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ActivityNowInput) (*mcp.CallToolResult, ActivityNowOutput, error) {
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, ActivityNowOutput{}, err
		}
		current := now().UTC()
		from := current.Add(-time.Hour)
		records, lastSeen, err := repository.ActivityRecords(ctx, from, current)
		if err != nil {
			return nil, ActivityNowOutput{}, err
		}
		coding, err := repository.CodingRecords(ctx, from)
		if err != nil {
			return nil, ActivityNowOutput{}, err
		}
		result := activity.Build(records, from, current)
		labels, err := repository.ListActivityLabels(ctx)
		if err != nil {
			return nil, ActivityNowOutput{}, err
		}
		summaries := activity.Summarize(result, 10, store.LabelsForSummary(labels))
		wake, err := store.LoadWakeTime(ctx, repository, current)
		if err != nil {
			return nil, ActivityNowOutput{}, err
		}
		output := ActivityNowOutput{Now: current, Timezone: location.String(), Notes: activityNotes, OpenIncidents: []store.CollectionIncident{},
			Coding: activity.CodingThreads(coding, from, current)}
		if wake != nil {
			output.WokeAt = wake.In(location).Format("2006-01-02 15:04")
		}
		incidents, err := repository.ListCollectionIncidents(ctx, current, 50)
		if err != nil {
			return nil, ActivityNowOutput{}, err
		}
		for _, incident := range incidents {
			if incident.EndedAt == nil {
				output.OpenIncidents = append(output.OpenIncidents, incident)
			}
		}
		for _, summary := range summaries {
			device := DeviceNow{Device: summary.Device, RecentApps: summary.Apps, LastHourActive: summary.ActiveMinutes}
			if last, ok := lastSeen[summary.Device]; ok {
				device.LastRecord = &last
				minutes := roundMinutes(current.Sub(last).Minutes())
				device.MinutesSinceRecord = &minutes
			}
			for i := len(result.Segments) - 1; i >= 0; i-- {
				segment := result.Segments[i]
				if segment.Device == summary.Device {
					// Current only if it reaches the device's newest evidence.
					if current.Sub(segment.End) <= 2*time.Minute {
						device.Current = &TimelineSegment{Segment: segment, Minutes: roundMinutes(segment.Duration().Minutes())}
					}
					break
				}
			}
			output.Devices = append(output.Devices, device)
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "activity_summary",
		Description: "Where the owner's time went: active minutes per device, ranked apps, browser sites and window titles for a day or range. " + activityNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ActivitySummaryInput) (*mcp.CallToolResult, ActivitySummaryOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 15
		}
		if limit < 1 || limit > 100 {
			return nil, ActivitySummaryOutput{}, errors.New("limit must be between 1 and 100")
		}
		from, to, location, err := activityRange(ctx, repository, input.ActivityRangeInput, now())
		if err != nil {
			return nil, ActivitySummaryOutput{}, err
		}
		records, lastSeen, err := repository.ActivityRecords(ctx, from, to)
		if err != nil {
			return nil, ActivitySummaryOutput{}, err
		}
		result := activity.Build(records, from, to)
		for device, last := range lastSeen {
			result.LastRecord[device] = last
		}
		labels, err := repository.ListActivityLabels(ctx)
		if err != nil {
			return nil, ActivitySummaryOutput{}, err
		}
		return nil, ActivitySummaryOutput{From: from, To: to, Timezone: location.String(), Devices: activity.Summarize(result, limit, store.LabelsForSummary(labels)), NoData: result.Gaps, Notes: activityNotes}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "activity_timeline",
		Description: "The owner's activity in time order: one segment per continuous use of an app (and site, in a browser) on the Mac or phone. " + activityNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ActivityTimelineInput) (*mcp.CallToolResult, ActivityTimelineOutput, error) {
		minSeconds, limit := input.MinSeconds, input.Limit
		if minSeconds == 0 {
			minSeconds = 30
		}
		if limit == 0 {
			limit = 400
		}
		if minSeconds < 0 || limit < 1 || limit > 2000 {
			return nil, ActivityTimelineOutput{}, errors.New("min_seconds must not be negative and limit must be between 1 and 2000")
		}
		from, to, location, err := activityRange(ctx, repository, input.ActivityRangeInput, now())
		if err != nil {
			return nil, ActivityTimelineOutput{}, err
		}
		records, _, err := repository.ActivityRecords(ctx, from, to)
		if err != nil {
			return nil, ActivityTimelineOutput{}, err
		}
		result := activity.Build(records, from, to)
		output := ActivityTimelineOutput{From: from, To: to, Timezone: location.String(), Segments: []TimelineSegment{}, NoData: result.Gaps, Notes: activityNotes}
		for _, segment := range result.Segments {
			if segment.Duration() < time.Duration(minSeconds)*time.Second {
				output.Omitted++
				continue
			}
			if len(output.Segments) == limit {
				output.Truncated = true
				break
			}
			output.Segments = append(output.Segments, TimelineSegment{Segment: segment, Minutes: roundMinutes(segment.Duration().Minutes())})
		}
		return nil, output, nil
	})
}

// ownerLocation is the timezone of the approved policy, which defaults to
// the owner's configured zone.
func ownerLocation(ctx context.Context, repository store.Repository) (*time.Location, error) {
	config := policy.Config{}
	stored, err := repository.LatestPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if stored != nil {
		config = stored.Config
	}
	if err := config.Normalize(); err != nil {
		return nil, err
	}
	return time.LoadLocation(config.Timezone)
}

const maxActivityRange = 8 * 24 * time.Hour

func activityRange(ctx context.Context, repository store.Repository, input ActivityRangeInput, current time.Time) (time.Time, time.Time, *time.Location, error) {
	location, err := ownerLocation(ctx, repository)
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	current = current.UTC()
	var from, to time.Time
	switch {
	case input.From != "":
		value, err := mcpTime(input.From)
		if err != nil {
			return from, to, nil, fmt.Errorf("from: %w", err)
		}
		from, to = *value, current
	default:
		day := current.In(location)
		if input.Date != "" {
			day, err = time.ParseInLocation("2006-01-02", input.Date, location)
			if err != nil {
				return from, to, nil, errors.New("date must be YYYY-MM-DD")
			}
		}
		from = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location).UTC()
		to = from.AddDate(0, 0, 1)
	}
	if input.To != "" {
		value, err := mcpTime(input.To)
		if err != nil {
			return from, to, nil, fmt.Errorf("to: %w", err)
		}
		to = *value
	}
	if to.After(current) {
		to = current
	}
	if !to.After(from) {
		return from, to, nil, errors.New("the range must end after it starts and not lie entirely in the future")
	}
	if to.Sub(from) > maxActivityRange {
		return from, to, nil, errors.New("the range must be at most 8 days")
	}
	return from, to, location, nil
}

func roundMinutes(minutes float64) float64 { return float64(int64(minutes*10+0.5)) / 10 }
