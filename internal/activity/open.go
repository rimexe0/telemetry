package activity

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// OpenSpan is a stretch in which a T3 Code client was visible showing a
// thread in a project folder, as that client reported to its T3 server.
// It is what shows a switch between projects in T3 Code, whose window title
// is the same for every thread.
type OpenSpan struct {
	// Kind is the client's kind: "desktop-renderer", "web" or "mobile".
	Kind  string
	Start time.Time
	End   time.Time
	// Root is the folder the thread works in; Project is the T3 project it
	// belongs to, when known.
	Root    string
	Project string
}

// OpenSpans reads t3.open records, naming each folder's project from the
// same machine's T3 projects and thread worktrees.
func OpenSpans(coding []Record) []OpenSpan {
	type project struct{ title, root string }
	projects := map[string]map[string]project{}
	folders := map[string][]project{}
	for _, record := range coding {
		switch record.Kind {
		case "t3.project":
			var payload struct {
				ID    string `json:"project_id"`
				Title string `json:"title"`
				Root  string `json:"workspace_root"`
			}
			if json.Unmarshal(record.Payload, &payload) == nil {
				if projects[record.Source] == nil {
					projects[record.Source] = map[string]project{}
				}
				projects[record.Source][payload.ID] = project{payload.Title, payload.Root}
				folders[record.Source] = append(folders[record.Source], project{payload.Title, payload.Root})
			}
		}
	}
	for _, record := range coding {
		if record.Kind != "t3.thread" {
			continue
		}
		var payload struct {
			ProjectID string `json:"project_id"`
			Worktree  string `json:"worktree_path"`
		}
		if json.Unmarshal(record.Payload, &payload) == nil && payload.Worktree != "" {
			if owner, ok := projects[record.Source][payload.ProjectID]; ok {
				folders[record.Source] = append(folders[record.Source], project{owner.title, payload.Worktree})
			}
		}
	}
	spans := []OpenSpan{}
	for _, record := range coding {
		if record.Kind != "t3.open" || record.End == nil {
			continue
		}
		var payload struct {
			Kind string `json:"client_kind"`
			Cwd  string `json:"cwd"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil || payload.Cwd == "" {
			continue
		}
		// A client showing several folders at once names none of them.
		if strings.Contains(payload.Cwd, "\n") {
			continue
		}
		span := OpenSpan{Kind: payload.Kind, Start: record.Start, End: *record.End, Root: payload.Cwd}
		best := 0
		for _, folder := range folders[record.Source] {
			if folder.root != "" && (payload.Cwd == folder.root || strings.HasPrefix(payload.Cwd, strings.TrimRight(folder.root, "/")+"/")) && len(folder.root) > best {
				span.Project, best = folder.title, len(folder.root)
			}
		}
		if span.Project == "" {
			span.Project = payload.Cwd[strings.LastIndex(strings.TrimRight(payload.Cwd, "/"), "/")+1:]
		}
		spans = append(spans, span)
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	return spans
}

// openKind is the kind of T3 client that runs on a device.
func openKind(device string) string {
	if device == DevicePhone {
		return "mobile"
	}
	return "desktop-renderer"
}

// openFor returns the span that covers most of a T3 Code segment, if any.
func openFor(segment Segment, spans []OpenSpan) *OpenSpan {
	if agentTool(segment.App) != "t3" {
		return nil
	}
	var best *OpenSpan
	var most time.Duration
	for i := range spans {
		span := &spans[i]
		if span.Kind != openKind(segment.Device) {
			continue
		}
		if overlap := earlier(span.End, segment.End).Sub(later(span.Start, segment.Start)); overlap > most {
			best, most = span, overlap
		}
	}
	return best
}
