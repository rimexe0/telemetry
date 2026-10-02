package mcpapi

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/store"
)

const patternNotes = "How the owner actually does each task, from the records of the last days: when they get to it, how long they stay, what they leave it for, and whether they come back. " +
	"These are this owner's own patterns, not general ones, and they change; use them to plan with real durations and to see what pulls them away. " +
	"Coming back is what matters, so read left_to together with came_back. Report them as facts and never as failures. A task with no rules or memory has no visits. " +
	"Days with missing data undercount; missing data is never rest or focus."

type TaskPatternsInput struct {
	Days int `json:"days,omitempty" jsonschema:"how many days back including today, 1 through 14; default 7"`
}

type LeftTo struct {
	Context string `json:"context" jsonschema:"what they went to; empty when they left the devices"`
	Kind    string `json:"kind" jsonschema:"distraction, task (another task), other or away"`
	Count   int    `json:"count"`
}

type TaskPattern struct {
	TaskID               int64    `json:"task_id"`
	Task                 string   `json:"task"`
	DaysWorked           int      `json:"days_worked"`
	Visits               int      `json:"visits"`
	Minutes              float64  `json:"minutes" jsonschema:"the owner's own attention"`
	TypicalVisitMinutes  float64  `json:"typical_visit_minutes" jsonschema:"median length of one stretch on the task"`
	LongestVisitMinutes  float64  `json:"longest_visit_minutes"`
	UsualStart           string   `json:"usual_start,omitempty" jsonschema:"median local time of the first visit on a day it was worked on"`
	Left                 int      `json:"left" jsonschema:"times a visit ended"`
	CameBack             int      `json:"came_back" jsonschema:"of those, how often they returned the same day"`
	TypicalMinutesToBack float64  `json:"typical_minutes_to_come_back" jsonschema:"median, over the returns"`
	LeftTo               []LeftTo `json:"left_to" jsonschema:"what they most often went to, up to 6"`
	AgentMinutes         float64  `json:"agent_minutes"`
	WaitingMinutes       float64  `json:"waiting_minutes" jsonschema:"time its agent sat stopped until the owner came back"`
}

type TaskPatternsOutput struct {
	From     string        `json:"from" jsonschema:"first local day"`
	To       string        `json:"to" jsonschema:"last local day"`
	Timezone string        `json:"timezone"`
	Tasks    []TaskPattern `json:"tasks"`
	Notes    string        `json:"notes"`
}

func addPatternTools(server *mcp.Server, repository store.Repository, now func() time.Time) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "task_patterns",
		Description: patternNotes,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input TaskPatternsInput) (*mcp.CallToolResult, TaskPatternsOutput, error) {
		days := input.Days
		if days == 0 {
			days = 7
		}
		if days < 1 || days > 14 {
			return nil, TaskPatternsOutput{}, errors.New("days must be between 1 and 14")
		}
		location, err := ownerLocation(ctx, repository)
		if err != nil {
			return nil, TaskPatternsOutput{}, err
		}
		current := now().UTC()
		today := current.In(location)
		type gathered struct {
			pattern  TaskPattern
			visits   []float64
			starts   []float64
			backs    []float64
			leftTo   map[string]*LeftTo
			seenDays int
		}
		byTask := map[int64]*gathered{}
		order := []int64{}
		first := today.AddDate(0, 0, -(days - 1))
		for offset := 0; offset < days; offset++ {
			day := first.AddDate(0, 0, offset)
			from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location).UTC()
			to := from.AddDate(0, 0, 1)
			if to.After(current) {
				to = current
			}
			if !to.After(from) {
				continue
			}
			loaded, _, err := store.LoadDayInput(ctx, repository, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), from, to, false)
			if err != nil {
				return nil, TaskPatternsOutput{}, err
			}
			built := activity.BuildDay(loaded)
			visits := activity.TaskVisits(loaded)
			for _, task := range built.Tasks {
				item := byTask[task.ID]
				if item == nil {
					item = &gathered{pattern: TaskPattern{TaskID: task.ID, Task: task.Title, LeftTo: []LeftTo{}}, leftTo: map[string]*LeftTo{}}
					byTask[task.ID] = item
					order = append(order, task.ID)
				}
				item.pattern.Minutes += task.Minutes
				item.pattern.AgentMinutes += task.AgentMinutes
				item.pattern.WaitingMinutes += task.WaitingMinutes
				list := visits[task.ID]
				if len(list) == 0 {
					continue
				}
				item.pattern.DaysWorked++
				local := list[0].Start.In(location)
				item.starts = append(item.starts, float64(local.Hour()*60+local.Minute()))
				for i, visit := range list {
					minutes := visit.End.Sub(visit.Start).Minutes()
					item.pattern.Visits++
					item.visits = append(item.visits, minutes)
					if minutes > item.pattern.LongestVisitMinutes {
						item.pattern.LongestVisitMinutes = minutes
					}
					// The visit still running now has not been left.
					if i == len(list)-1 && to.Sub(visit.End) < 2*time.Minute {
						continue
					}
					item.pattern.Left++
					if visit.Back != nil {
						item.pattern.CameBack++
						item.backs = append(item.backs, visit.Back.Sub(visit.End).Minutes())
					}
					key := visit.LeftKind + "\x00" + visit.LeftTo
					if item.leftTo[key] == nil {
						item.leftTo[key] = &LeftTo{Context: visit.LeftTo, Kind: visit.LeftKind}
					}
					item.leftTo[key].Count++
				}
			}
		}
		output := TaskPatternsOutput{From: first.Format("2006-01-02"), To: today.Format("2006-01-02"), Timezone: location.String(), Tasks: []TaskPattern{}, Notes: patternNotes}
		for _, id := range order {
			item := byTask[id]
			pattern := item.pattern
			pattern.Minutes, pattern.AgentMinutes, pattern.WaitingMinutes = roundMinutes(pattern.Minutes), roundMinutes(pattern.AgentMinutes), roundMinutes(pattern.WaitingMinutes)
			pattern.TypicalVisitMinutes, pattern.LongestVisitMinutes = roundMinutes(activity.Median(item.visits)), roundMinutes(pattern.LongestVisitMinutes)
			pattern.TypicalMinutesToBack = roundMinutes(activity.Median(item.backs))
			if len(item.starts) > 0 {
				minute := int(activity.Median(item.starts))
				pattern.UsualStart = time.Date(2000, 1, 1, minute/60, minute%60, 0, 0, time.UTC).Format("15:04")
			}
			for _, left := range item.leftTo {
				pattern.LeftTo = append(pattern.LeftTo, *left)
			}
			sort.Slice(pattern.LeftTo, func(i, j int) bool {
				if pattern.LeftTo[i].Count != pattern.LeftTo[j].Count {
					return pattern.LeftTo[i].Count > pattern.LeftTo[j].Count
				}
				return pattern.LeftTo[i].Context < pattern.LeftTo[j].Context
			})
			if len(pattern.LeftTo) > 6 {
				pattern.LeftTo = pattern.LeftTo[:6]
			}
			output.Tasks = append(output.Tasks, pattern)
		}
		return nil, output, nil
	})
}
