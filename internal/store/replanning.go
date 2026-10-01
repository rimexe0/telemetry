package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"
	ProposalStale    = "stale"

	proposalOriginDisruption = "disruption_helper"
	proposalOriginExplicit   = "explicit"

	ChangeUnchanged     = "unchanged"
	ChangeMoved         = "moved"
	ChangeResized       = "resized"
	ChangeParked        = "parked"
	ChangeAdded         = "added"
	ChangeFixedConflict = "fixed_conflict"

	maxProposalListLimit = 50
)

var (
	ErrInvalidProposal  = errors.New("invalid plan change proposal")
	ErrProposalStale    = errors.New("plan changed after the proposal was prepared")
	ErrProposalConflict = errors.New("plan change proposal conflict")
)

// ProposedBlock is one block of the exact plan a proposal would save.
// SourceBlockID names the base-version block it continues, if any; blocks
// without one are new. State is carried from the base block so completed work
// is not reset to planned by a replan.
type ProposedBlock struct {
	SourceBlockID   *int64    `json:"source_block_id,omitempty"`
	ActivityID      *int64    `json:"activity_id,omitempty"`
	Title           string    `json:"title"`
	StartTime       time.Time `json:"start_time"`
	DurationMinutes int       `json:"duration_minutes"`
	Fixed           bool      `json:"fixed"`
	Focus           bool      `json:"focus,omitempty"`
	State           string    `json:"state"`
}

type ProposedPlan struct {
	Note   string          `json:"note"`
	Blocks []ProposedBlock `json:"blocks"`
}

// PlanChange describes one block-level difference in owner-readable terms.
type PlanChange struct {
	Kind          string     `json:"kind"`
	BaseBlockID   *int64     `json:"base_block_id,omitempty"`
	ActivityID    *int64     `json:"activity_id,omitempty"`
	Title         string     `json:"title"`
	Fixed         bool       `json:"fixed"`
	State         string     `json:"state,omitempty"`
	OldStart      *time.Time `json:"old_start,omitempty"`
	OldDuration   *int       `json:"old_duration_minutes,omitempty"`
	NewStart      *time.Time `json:"new_start,omitempty"`
	NewDuration   *int       `json:"new_duration_minutes,omitempty"`
	Detail        string     `json:"detail,omitempty"`
	RelatedBlocks []int64    `json:"related_block_ids,omitempty"`
}

// ProposalWarning reports missing context the owner should see before
// accepting, such as unfinished work without a resume marker.
type ProposalWarning struct {
	Code        string `json:"code"`
	BaseBlockID *int64 `json:"base_block_id,omitempty"`
	SessionID   *int64 `json:"session_id,omitempty"`
	Detail      string `json:"detail"`
}

// DisruptionInput asks the helper to move unfinished movable work out of a
// disrupted interval. AvailableUntil is required: no end of day is assumed.
type DisruptionInput struct {
	Start          time.Time  `json:"start"`
	End            *time.Time `json:"end,omitempty"`
	AvailableUntil *time.Time `json:"available_until,omitempty"`
}

type PlanChangeRequest struct {
	BasePlanVersionID int64            `json:"base_plan_version_id"`
	Reason            string           `json:"reason"`
	Evidence          json.RawMessage  `json:"evidence,omitempty"`
	Disruption        *DisruptionInput `json:"disruption,omitempty"`
	Plan              *ProposedPlan    `json:"plan,omitempty"`
}

type PlanChangeProposal struct {
	ID                  int64             `json:"id"`
	PlanDate            string            `json:"plan_date"`
	BasePlanVersionID   int64             `json:"base_plan_version_id"`
	Origin              string            `json:"origin"`
	Reason              string            `json:"reason"`
	Request             json.RawMessage   `json:"request"`
	ProposedPlan        ProposedPlan      `json:"proposed_plan"`
	Changes             []PlanChange      `json:"changes"`
	Warnings            []ProposalWarning `json:"warnings"`
	ContentHash         string            `json:"content_hash"`
	Status              string            `json:"status"`
	CreatedAt           time.Time         `json:"created_at"`
	DecidedAt           *time.Time        `json:"decided_at,omitempty"`
	DecisionNote        string            `json:"decision_note,omitempty"`
	DecisionProvenance  json.RawMessage   `json:"decision_provenance,omitempty"`
	ResultPlanVersionID *int64            `json:"result_plan_version_id,omitempty"`
}

type ProposalDecision struct {
	ContentHash string `json:"content_hash"`
	Note        string `json:"note,omitempty"`
}

var unfinishedBlockStates = map[string]struct{}{"planned": {}, "active": {}}

func blockEndTime(start time.Time, minutes int) time.Time {
	return start.Add(time.Duration(minutes) * time.Minute)
}

func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func sortedBaseBlocks(blocks []PlanBlock) []PlanBlock {
	sorted := append([]PlanBlock(nil), blocks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].StartTime.Equal(sorted[j].StartTime) {
			return sorted[i].StartTime.Before(sorted[j].StartTime)
		}
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}

func carried(block PlanBlock) ProposedBlock {
	id := block.ID
	return ProposedBlock{
		SourceBlockID: &id, ActivityID: block.ActivityID, Title: block.Title,
		StartTime: block.StartTime, DurationMinutes: block.DurationMinutes, Fixed: block.Fixed, Focus: block.Focus, State: block.State,
	}
}

// DisruptionReplan is the deliberately mechanical helper. Fixed blocks and
// finished blocks keep their exact times. Unfinished movable blocks that end
// after the disruption starts move, in their existing order and with their
// existing duration, into the earliest gap after the disruption that crosses
// neither a fixed block nor AvailableUntil. A block that cannot fit is parked
// (left out of the proposed plan and reported), never shortened or dropped
// silently.
func DisruptionReplan(base Plan, input DisruptionInput) (ProposedPlan, []PlanChange, error) {
	if input.Start.IsZero() {
		return ProposedPlan{}, nil, fmt.Errorf("%w: disruption start is required", ErrInvalidProposal)
	}
	if input.AvailableUntil == nil {
		return ProposedPlan{}, nil, fmt.Errorf("%w: available_until is required because no end of day is configured", ErrInvalidProposal)
	}
	end := input.Start
	if input.End != nil {
		end = *input.End
	}
	if end.Before(input.Start) {
		return ProposedPlan{}, nil, fmt.Errorf("%w: disruption end cannot be before its start", ErrInvalidProposal)
	}
	if !input.AvailableUntil.After(end) {
		return ProposedPlan{}, nil, fmt.Errorf("%w: available_until must be after the disruption ends", ErrInvalidProposal)
	}

	type interval struct{ start, end time.Time }
	var occupied []interval
	blocks := sortedBaseBlocks(base.Blocks)
	for _, block := range blocks {
		if block.Fixed {
			occupied = append(occupied, interval{block.StartTime, blockEndTime(block.StartTime, block.DurationMinutes)})
		}
	}
	free := func(start, finish time.Time) (bool, time.Time) {
		for _, slot := range occupied {
			if overlaps(start, finish, slot.start, slot.end) {
				return false, slot.end
			}
		}
		return true, time.Time{}
	}

	proposed := ProposedPlan{Blocks: make([]ProposedBlock, 0, len(blocks))}
	parked := map[int64]bool{}
	cursor := end
	for _, block := range blocks {
		_, unfinished := unfinishedBlockStates[block.State]
		blockEnd := blockEndTime(block.StartTime, block.DurationMinutes)
		if block.Fixed || !unfinished || !blockEnd.After(input.Start) {
			proposed.Blocks = append(proposed.Blocks, carried(block))
			continue
		}
		start := block.StartTime
		if start.Before(cursor) {
			start = cursor
		}
		placed := false
		for {
			finish := blockEndTime(start, block.DurationMinutes)
			if finish.After(*input.AvailableUntil) {
				break
			}
			ok, next := free(start, finish)
			if ok {
				placed = true
				break
			}
			start = next
		}
		if !placed {
			parked[block.ID] = true
			continue
		}
		next := carried(block)
		next.StartTime = start
		if !start.Equal(block.StartTime) {
			next.State = "planned"
		}
		proposed.Blocks = append(proposed.Blocks, next)
		finish := blockEndTime(start, block.DurationMinutes)
		occupied = append(occupied, interval{start, finish})
		cursor = finish
	}
	changes := DiffPlan(base, proposed)
	for i := range changes {
		if changes[i].Kind == ChangeParked && changes[i].BaseBlockID != nil && parked[*changes[i].BaseBlockID] {
			changes[i].Detail = "no gap before available_until without crossing a fixed block"
		}
	}
	return proposed, changes, nil
}

// DiffPlan compares the base version with an exact proposed plan. Output is
// ordered by base block order, then new blocks in proposed order, then fixed
// conflicts, so the same inputs always produce the same diff.
func DiffPlan(base Plan, proposed ProposedPlan) []PlanChange {
	bySource := map[int64]ProposedBlock{}
	for _, block := range proposed.Blocks {
		if block.SourceBlockID != nil {
			bySource[*block.SourceBlockID] = block
		}
	}
	changes := make([]PlanChange, 0, len(base.Blocks)+len(proposed.Blocks))
	var conflicts []PlanChange
	for _, block := range sortedBaseBlocks(base.Blocks) {
		id := block.ID
		oldStart, oldDuration := block.StartTime, block.DurationMinutes
		change := PlanChange{BaseBlockID: &id, ActivityID: block.ActivityID, Title: block.Title, Fixed: block.Fixed, State: block.State, OldStart: &oldStart, OldDuration: &oldDuration}
		next, kept := bySource[block.ID]
		if !kept {
			change.Kind = ChangeParked
			changes = append(changes, change)
			if block.Fixed {
				conflicts = append(conflicts, PlanChange{Kind: ChangeFixedConflict, BaseBlockID: &id, Title: block.Title, Fixed: true, OldStart: &oldStart, OldDuration: &oldDuration, Detail: "fixed block removed"})
			}
			continue
		}
		newStart, newDuration := next.StartTime, next.DurationMinutes
		change.NewStart, change.NewDuration = &newStart, &newDuration
		switch {
		case newDuration != oldDuration:
			change.Kind = ChangeResized
		case !newStart.Equal(oldStart):
			change.Kind = ChangeMoved
		default:
			change.Kind = ChangeUnchanged
		}
		var edits []string
		if next.Title != block.Title {
			edits = append(edits, fmt.Sprintf("title %q -> %q", block.Title, next.Title))
		}
		if !sameActivity(next.ActivityID, block.ActivityID) {
			edits = append(edits, "activity changed")
		}
		if next.Fixed != block.Fixed {
			edits = append(edits, fmt.Sprintf("fixed %t -> %t", block.Fixed, next.Fixed))
		}
		if len(edits) > 0 {
			change.Detail = strings.Join(edits, "; ")
			if change.Kind == ChangeUnchanged {
				change.Kind = ChangeMoved
			}
		}
		changes = append(changes, change)
		if block.Fixed && change.Kind != ChangeUnchanged {
			conflicts = append(conflicts, PlanChange{Kind: ChangeFixedConflict, BaseBlockID: &id, Title: block.Title, Fixed: true, OldStart: &oldStart, OldDuration: &oldDuration, NewStart: &newStart, NewDuration: &newDuration, Detail: "fixed block changed"})
		}
	}
	for _, block := range proposed.Blocks {
		if block.SourceBlockID != nil {
			continue
		}
		start, duration := block.StartTime, block.DurationMinutes
		changes = append(changes, PlanChange{Kind: ChangeAdded, ActivityID: block.ActivityID, Title: block.Title, Fixed: block.Fixed, NewStart: &start, NewDuration: &duration})
	}
	// Overlap among movable blocks is an allowed interleave; two fixed
	// commitments overlapping is a conflict the owner must see.
	for i, a := range proposed.Blocks {
		for _, b := range proposed.Blocks[i+1:] {
			if !a.Fixed || !b.Fixed {
				continue
			}
			if overlaps(a.StartTime, blockEndTime(a.StartTime, a.DurationMinutes), b.StartTime, blockEndTime(b.StartTime, b.DurationMinutes)) {
				start, duration := b.StartTime, b.DurationMinutes
				conflicts = append(conflicts, PlanChange{Kind: ChangeFixedConflict, Title: b.Title, Fixed: true, NewStart: &start, NewDuration: &duration, Detail: fmt.Sprintf("overlaps fixed block %q", a.Title), RelatedBlocks: sourceIDs(a, b)})
			}
		}
	}
	return append(changes, conflicts...)
}

func sourceIDs(blocks ...ProposedBlock) []int64 {
	ids := []int64{}
	for _, block := range blocks {
		if block.SourceBlockID != nil {
			ids = append(ids, *block.SourceBlockID)
		}
	}
	return ids
}

func sameActivity(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ResumeWarnings reports unfinished open sessions attached to blocks the
// proposal moves or parks when no resume marker exists. It never fabricates one.
func ResumeWarnings(changes []PlanChange, sessions []ExecutionSession, activities map[int64]Activity) []ProposalWarning {
	warnings := []ProposalWarning{}
	affected := map[int64]PlanChange{}
	for _, change := range changes {
		if change.BaseBlockID != nil && (change.Kind == ChangeMoved || change.Kind == ChangeParked || change.Kind == ChangeResized) {
			affected[*change.BaseBlockID] = change
		}
	}
	for _, session := range sessions {
		if session.EndedAt != nil || session.PlanBlockID == nil {
			continue
		}
		change, ok := affected[*session.PlanBlockID]
		if !ok {
			continue
		}
		marker := ""
		if session.ResumeMarker != nil {
			marker = strings.TrimSpace(*session.ResumeMarker)
		}
		if marker == "" {
			marker = strings.TrimSpace(activities[session.ActivityID].ResumeMarker)
		}
		if marker != "" {
			continue
		}
		sessionID, blockID := session.ID, *session.PlanBlockID
		warnings = append(warnings, ProposalWarning{
			Code: "missing_resume_marker", BaseBlockID: &blockID, SessionID: &sessionID,
			Detail: fmt.Sprintf("%q is %s but its open execution session has no resume marker; add one before accepting to keep return context", change.Title, change.Kind),
		})
	}
	return warnings
}

// ProposalContentHash covers exactly what acceptance would save. Accepting
// requires the caller to echo this hash, so a different proposal cannot be
// approved by reference to an earlier review.
func ProposalContentHash(planDate string, baseVersionID int64, plan ProposedPlan) (string, error) {
	blocks := make([]map[string]any, len(plan.Blocks))
	for i, block := range plan.Blocks {
		blocks[i] = map[string]any{
			"source_block_id": block.SourceBlockID, "activity_id": block.ActivityID, "title": block.Title,
			"start_time": block.StartTime.UTC().Format(time.RFC3339Nano), "duration_minutes": block.DurationMinutes,
			"fixed": block.Fixed, "state": block.State,
		}
		// Only present when set, so hashes of earlier proposals stay valid.
		if block.Focus {
			blocks[i]["focus"] = true
		}
	}
	payload, err := json.Marshal(map[string]any{"plan_date": planDate, "base_plan_version_id": baseVersionID, "note": plan.Note, "blocks": blocks})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// validateProposedPlan applies SavePlan's block rules to an explicit proposal
// and requires every source block to belong to the base version.
func validateProposedPlan(base Plan, plan *ProposedPlan) error {
	if len(plan.Blocks) > maxPlanningBlocks {
		return fmt.Errorf("%w: a plan may contain at most %d blocks", ErrInvalidProposal, maxPlanningBlocks)
	}
	if len([]rune(plan.Note)) > maxPlanningTextRunes {
		return fmt.Errorf("%w: note must be at most %d characters", ErrInvalidProposal, maxPlanningTextRunes)
	}
	baseBlocks := map[int64]PlanBlock{}
	for _, block := range base.Blocks {
		baseBlocks[block.ID] = block
	}
	seen := map[int64]bool{}
	for i := range plan.Blocks {
		block := &plan.Blocks[i]
		block.Title = strings.TrimSpace(block.Title)
		if block.SourceBlockID != nil {
			source, ok := baseBlocks[*block.SourceBlockID]
			if !ok {
				return fmt.Errorf("%w: block %d source_block_id is not in the base plan version", ErrInvalidProposal, i+1)
			}
			if seen[*block.SourceBlockID] {
				return fmt.Errorf("%w: block %d repeats source_block_id %d", ErrInvalidProposal, i+1, *block.SourceBlockID)
			}
			seen[*block.SourceBlockID] = true
			if block.State == "" {
				block.State = source.State
				if !block.StartTime.Equal(source.StartTime) || block.DurationMinutes != source.DurationMinutes {
					block.State = "planned"
				}
			}
		}
		if block.State == "" {
			block.State = "planned"
		}
		if _, ok := planBlockStates[block.State]; !ok {
			return fmt.Errorf("%w: block %d state is not recognized", ErrInvalidProposal, i+1)
		}
		if block.ActivityID != nil && *block.ActivityID <= 0 {
			return fmt.Errorf("%w: block %d activity_id must reference an activity", ErrInvalidProposal, i+1)
		}
		if block.Title == "" && block.ActivityID == nil {
			return fmt.Errorf("%w: block %d needs a title or an activity", ErrInvalidProposal, i+1)
		}
		if len([]rune(block.Title)) > maxPlanningTextRunes {
			return fmt.Errorf("%w: block %d title must be at most %d characters", ErrInvalidProposal, i+1, maxPlanningTextRunes)
		}
		if block.DurationMinutes < 1 || block.DurationMinutes > 1440 {
			return fmt.Errorf("%w: block %d duration_minutes must be between 1 and 1440", ErrInvalidProposal, i+1)
		}
		if block.StartTime.IsZero() {
			return fmt.Errorf("%w: block %d start_time is required", ErrInvalidProposal, i+1)
		}
	}
	return nil
}

// PreparePlanChange builds the exact proposed plan, diff and hash from a
// request against the base version. It has no side effects.
func PreparePlanChange(base Plan, request PlanChangeRequest) (origin string, plan ProposedPlan, changes []PlanChange, err error) {
	if request.BasePlanVersionID <= 0 || request.BasePlanVersionID != base.VersionID {
		return "", ProposedPlan{}, nil, fmt.Errorf("%w: base_plan_version_id must be the current plan version", ErrProposalStale)
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len([]rune(request.Reason)) > maxPlanningTextRunes {
		return "", ProposedPlan{}, nil, fmt.Errorf("%w: reason is required and must be at most %d characters", ErrInvalidProposal, maxPlanningTextRunes)
	}
	if (request.Disruption == nil) == (request.Plan == nil) {
		return "", ProposedPlan{}, nil, fmt.Errorf("%w: supply exactly one of disruption or plan", ErrInvalidProposal)
	}
	if request.Disruption != nil {
		plan, changes, err = DisruptionReplan(base, *request.Disruption)
		if err != nil {
			return "", ProposedPlan{}, nil, err
		}
		plan.Note = request.Reason
		return proposalOriginDisruption, plan, changes, nil
	}
	plan = *request.Plan
	plan.Blocks = append([]ProposedBlock(nil), plan.Blocks...)
	if err := validateProposedPlan(base, &plan); err != nil {
		return "", ProposedPlan{}, nil, err
	}
	if plan.Note == "" {
		plan.Note = request.Reason
	}
	return proposalOriginExplicit, plan, DiffPlan(base, plan), nil
}
