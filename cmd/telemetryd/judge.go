package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/judge"
	"github.com/rimexe0/telemetry/internal/store"
)

// linkAfter is how long something outside every task stays on screen before
// the assistant is asked which task it belongs to.
const linkAfter = 3 * time.Minute

// linkReason marks memory left by the which-task question.
const linkReason = "which task: "

// runJudge asks the configured assistant about things nothing else decides,
// once each, and stores the answer as the task's memory.
//
// In a focus session: is what is on screen part of the task? "Unsure" is
// stored too, so the assistant is not asked again and the owner is.
//
// Outside one: which task, if any, does something the owner has stayed on
// belong to? A clear answer links it to that task, so the owner does not
// have to. Anything else is stored as unsure for every task, and the owner
// can say what it is when the system mentions it.
func runJudge(ctx context.Context, repository store.Repository, command []string) {
	if len(command) == 0 {
		return
	}
	asked := map[string]bool{}
	check := func() {
		now := time.Now().UTC()
		loadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		input, _, err := store.LoadDayInput(loadCtx, repository, now.Truncate(24*time.Hour), now.Add(-45*time.Minute), now, false)
		var evidence []store.TaskEvidence
		var tasks []store.Activity
		if err == nil {
			evidence, err = repository.ListTaskEvidence(loadCtx)
		}
		if err == nil {
			tasks, err = repository.ListActivities(loadCtx, store.ActivityFilter{Limit: 500})
		}
		cancel()
		if err != nil {
			log.Printf("judge: %v", err)
			return
		}
		tracking := activity.Track(input)
		focus := tracking.Focus
		if focus == nil {
			link(ctx, repository, command, asked, tracking, input, tasks, evidence, now)
			return
		}
		if focus.On != activity.FocusUnknown || focus.LearnContext == "" {
			return
		}
		key := strings.ToLower(focus.LearnContext) + "\x00" + focus.Since.String()
		if asked[key] {
			return
		}
		question := judge.Question{Task: focus.Task, NextAction: focus.NextAction, Context: focus.Context, Title: focus.Title}
		for _, task := range tasks {
			if task.ID == focus.TaskID {
				question.Notes = task.Notes
			}
		}
		for _, known := range evidence {
			if known.TaskID != focus.TaskID {
				continue
			}
			// An entry means the assistant was already asked about this for
			// this task. One left by the which-task question does not count:
			// that question knew less than this one.
			if strings.EqualFold(known.Context, focus.LearnContext) && !strings.HasPrefix(known.Reason, linkReason) {
				asked[key] = true
				return
			}
			switch known.Verdict {
			case store.EvidenceRelated:
				question.Related = append(question.Related, known.Context)
			case store.EvidenceUnrelated:
				question.Unrelated = append(question.Unrelated, known.Context)
			}
		}
		asked[key] = true
		askCtx, cancelAsk := context.WithTimeout(ctx, 2*time.Minute)
		verdict, err := judge.Ask(askCtx, command, question)
		cancelAsk()
		if err != nil {
			log.Printf("judge: %v", err)
			return
		}
		saveCtx, cancelSave := context.WithTimeout(ctx, 30*time.Second)
		defer cancelSave()
		if _, err := repository.AddTaskEvidence(saveCtx, store.TaskEvidence{TaskID: focus.TaskID, Context: focus.LearnContext, Verdict: verdict.Verdict,
			Source: store.EvidenceJudge, Reason: verdict.Reason}); err != nil {
			log.Printf("judge: %v", err)
			return
		}
		log.Printf("judge: %q for task %d is %s (%s)", focus.LearnContext, focus.TaskID, verdict.Verdict, verdict.Reason)
	}
	ticker := time.NewTicker(30 * time.Second)
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

// link asks which task the thing on screen belongs to, when the owner has
// stayed on it and no task knows it yet.
func link(ctx context.Context, repository store.Repository, command []string, asked map[string]bool, tracking activity.Tracking, input activity.DayInput,
	tasks []store.Activity, evidence []store.TaskEvidence, now time.Time) {
	learn := tracking.LearnContext
	if tracking.State != activity.KindOther || tracking.Excused || learn == "" || tracking.Since == nil || now.Sub(*tracking.Since) < linkAfter || len(input.Tasks) == 0 {
		return
	}
	key := "link\x00" + strings.ToLower(learn)
	if asked[key] {
		return
	}
	options := []judge.TaskOption{}
	for _, info := range input.Tasks {
		option := judge.TaskOption{ID: info.ID, Title: info.Title, Related: info.Related}
		for _, task := range tasks {
			if task.ID == info.ID {
				option.Notes = task.Notes
			}
		}
		options = append(options, option)
	}
	for _, known := range evidence {
		// Any entry for this thing means it was judged, or the owner spoke.
		if strings.EqualFold(known.Context, learn) {
			asked[key] = true
			return
		}
	}
	asked[key] = true
	askCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	answer, err := judge.AskLink(askCtx, command, options, tracking.Context, "")
	cancel()
	if err != nil {
		log.Printf("judge: %v", err)
		return
	}
	saveCtx, cancelSave := context.WithTimeout(ctx, 30*time.Second)
	defer cancelSave()
	linked := false
	for _, option := range options {
		verdict := store.EvidenceUnsure
		if answer.TaskID != nil && *answer.TaskID == option.ID && answer.Confidence == "high" {
			verdict, linked = store.EvidenceRelated, true
		}
		if _, err := repository.AddTaskEvidence(saveCtx, store.TaskEvidence{TaskID: option.ID, Context: learn, Verdict: verdict,
			Source: store.EvidenceJudge, Reason: linkReason + answer.Reason}); err != nil {
			log.Printf("judge: %v", err)
			return
		}
	}
	log.Printf("judge: %q linked=%v (%s)", learn, linked, answer.Reason)
}
