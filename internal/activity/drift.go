package activity

import (
	"sort"
	"time"
)

// DriftEpisode is a stretch of use spent mostly on distractions, with what
// came before it and whether the owner came back to work after. Coming back
// is what matters; an episode is a fact, never a failure.
type DriftEpisode struct {
	Device             string    `json:"device"`
	Start              time.Time `json:"start"`
	End                time.Time `json:"end"`
	ActiveMinutes      float64   `json:"active_minutes"`
	DistractionMinutes float64   `json:"distraction_minutes"`
	// On is what the time went to, most first.
	On     []DayItem `json:"on"`
	Before *ChunkRef `json:"before,omitempty"`
	After  *ChunkRef `json:"after,omitempty"`
	// CameBack is set when the next use, within an hour, was not on
	// distractions; BackToTask when it went to a task.
	CameBack   bool `json:"came_back"`
	BackToTask bool `json:"back_to_task"`
}

// ChunkRef names a neighbouring stretch of use.
type ChunkRef struct {
	Device string    `json:"device"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Kind   string    `json:"kind"`
	Task   string    `json:"task,omitempty"`
	// Top is what most of it went to.
	Top string `json:"top,omitempty"`
	// GapMinutes is the time between it and the episode.
	GapMinutes float64 `json:"gap_minutes"`
}

// cameBackWithin is how soon work must follow for an episode to count as
// come back from.
const cameBackWithin = time.Hour

// DriftEpisodes lists the day's distraction chunks of at least minMinutes
// active time, on any device, in time order.
func DriftEpisodes(day Day, minMinutes float64) []DriftEpisode {
	chunks := append([]Chunk(nil), day.Chunks...)
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Start.Before(chunks[j].Start) })
	episodes := []DriftEpisode{}
	for i, chunk := range chunks {
		if chunk.Kind != "distraction" || chunk.ActiveMinutes < minMinutes {
			continue
		}
		episode := DriftEpisode{Device: chunk.Device, Start: chunk.Start, End: chunk.End, ActiveMinutes: chunk.ActiveMinutes,
			DistractionMinutes: chunk.DistractionMinutes, On: topItems(chunk.Items, 3)}
		for j := i - 1; j >= 0; j-- {
			if chunks[j].End.After(chunk.Start) {
				continue
			}
			episode.Before = chunkRef(chunks[j], chunk.Start.Sub(chunks[j].End))
			break
		}
		for j := i + 1; j < len(chunks); j++ {
			if chunks[j].Start.Before(chunk.End) {
				continue
			}
			gap := chunks[j].Start.Sub(chunk.End)
			episode.After = chunkRef(chunks[j], gap)
			if gap <= cameBackWithin && chunks[j].Kind != "distraction" {
				episode.CameBack = true
				episode.BackToTask = chunks[j].Kind == "task"
			}
			break
		}
		episodes = append(episodes, episode)
	}
	return episodes
}

func chunkRef(chunk Chunk, gap time.Duration) *ChunkRef {
	ref := &ChunkRef{Device: chunk.Device, Start: chunk.Start, End: chunk.End, Kind: chunk.Kind, Task: chunk.Task, GapMinutes: round(gap.Minutes())}
	if top := topItems(chunk.Items, 1); len(top) > 0 {
		ref.Top = top[0].Name
		if top[0].Detail != "" {
			ref.Top += " · " + top[0].Detail
		}
	}
	return ref
}

func topItems(items []DayItem, limit int) []DayItem {
	sorted := append([]DayItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Minutes > sorted[j].Minutes })
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}
