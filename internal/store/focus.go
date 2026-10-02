package store

import (
	"context"
	"fmt"
)

// Focus actions the owner takes on a task.
const (
	FocusStart  = "start"
	FocusPause  = "pause"
	FocusResume = "resume"
	FocusDone   = "done"
)

// ChangeFocus starts, pauses, resumes or ends a focus session: a stretch in
// which the owner does one task only. It is the primary execution session
// (#1), so distractions are closed while it runs. Starting a session for
// another task ends the one that is open. Ending a session does not mark the
// task done; that is the owner's to say.
func ChangeFocus(ctx context.Context, repository Repository, action string, taskID int64) (*ExecutionSession, error) {
	current, err := repository.CurrentPrimaryExecutionSession(ctx)
	if err != nil {
		return nil, err
	}
	open := current != nil && current.EndedAt == nil
	transition := func(event, note string) (*ExecutionSession, error) {
		session, err := repository.TransitionExecutionSession(ctx, current.ID, ExecutionEventInput{EventType: event, Note: note})
		if err != nil {
			return nil, err
		}
		return &session, nil
	}
	switch action {
	case FocusStart:
		if open && current.ActivityID == taskID {
			if current.State == executionStateInterrupted {
				return transition(executionEventResumed, "")
			}
			return current, nil
		}
		if open {
			if _, err := transition(executionEventAbandoned, "Ended because a focus session for another task was started."); err != nil {
				return nil, err
			}
		}
		session, err := repository.StartExecutionSession(ctx, ExecutionSessionInput{ActivityID: taskID})
		if err != nil {
			return nil, err
		}
		return &session, nil
	case FocusPause:
		if !open || current.State == executionStateInterrupted {
			return current, nil
		}
		return transition(executionEventPaused, "")
	case FocusResume:
		if !open {
			return nil, fmt.Errorf("%w: no focus session is open", ErrInvalidExecutionTransition)
		}
		if current.State != executionStateInterrupted {
			return current, nil
		}
		return transition(executionEventResumed, "")
	case FocusDone:
		if !open {
			return current, nil
		}
		return transition(executionEventCompleted, "")
	}
	return nil, fmt.Errorf("%w: action must be start, pause, resume or done", ErrInvalidExecutionInput)
}
