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

// runJudge watches a running focus session. When what is on screen is
// neither known to the task nor decided by a rule, it asks the configured
// assistant once and stores the answer as the task's memory. "Unsure" is
// stored too, so the assistant is not asked again and the owner is.
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
		focus := activity.Track(input).Focus
		if focus == nil || focus.On != activity.FocusUnknown || focus.LearnContext == "" {
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
			// A stored "unsure" means the assistant was already asked about this.
			if strings.EqualFold(known.Context, focus.LearnContext) {
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
