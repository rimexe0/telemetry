package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/outreach"
	"github.com/rimexe0/telemetry/internal/store"
)

// runLoopholes hands each loophole case, once it has been quiet for
// store.LoopholeSettle, to the coding agent's webhook (a T3 Code task), and
// tells the owner about each report: once, then each hour until its fix
// merges (owner request 2026-11-22). The merge itself is the agent's: the
// server holds no GitHub access.
func runLoopholes(ctx context.Context, repository store.Repository, loopholes store.LoopholeStore, send []string) {
	client := &http.Client{Timeout: 30 * time.Second}
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		now := time.Now().UTC()
		dispatchLoopholes(checkCtx, client, loopholes, now)
		if len(send) > 0 {
			remindLoopholes(checkCtx, repository, loopholes, send, now)
		}
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

func dispatchLoopholes(ctx context.Context, client *http.Client, loopholes store.LoopholeStore, now time.Time) {
	webhook, err := loopholes.LoopholeWebhook(ctx)
	if err != nil {
		log.Printf("loopholes: %v", err)
		return
	}
	if webhook == "" {
		webhook = strings.TrimSpace(os.Getenv("TELEMETRY_LOOPHOLE_WEBHOOK"))
	}
	if webhook == "" {
		return
	}
	cases, err := loopholes.LoopholesToDispatch(ctx, now)
	if err != nil {
		log.Printf("loopholes: %v", err)
		return
	}
	for _, c := range cases {
		err := postLoophole(ctx, client, webhook, c)
		if markErr := loopholes.MarkLoopholeDispatched(ctx, c.ID, err); markErr != nil {
			log.Printf("loopholes: %v", markErr)
		}
		log.Printf("loopholes: case %d handed over: %v", c.ID, err)
	}
}

// postLoophole sends the case's number and a short summary; the agent reads
// the rest through MCP.
func postLoophole(ctx context.Context, client *http.Client, webhook string, c store.LoopholeCase) error {
	what := []string{}
	for _, event := range c.Events {
		what = append(what, event.At.Format(time.RFC3339)+" "+event.Source+": "+event.What)
	}
	body, err := json.Marshal(map[string]any{"event": "loophole_case", "case_id": c.ID, "opened_at": c.OpenedAt, "what": what})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 300))
		return fmt.Errorf("webhook answered %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

func remindLoopholes(ctx context.Context, repository store.Repository, loopholes store.LoopholeStore, send []string, now time.Time) {
	cases, err := loopholes.LoopholeCases(ctx, true, 50)
	if err != nil {
		log.Printf("loopholes: %v", err)
		return
	}
	location := time.UTC
	if _, loaded, err := outreachConfig(ctx, repository); err == nil {
		location = loaded
	}
	for _, c := range cases {
		if !c.ReminderDue(now) {
			continue
		}
		deliver(ctx, repository, send, outreach.KindLoophole, outreach.LoopholeMessage(c.ID, c.Message, c.PRURL, c.ThreadURL, c.MergeAfter, location),
			fmt.Sprintf("loophole %d reminder %d", c.ID, c.Reminders+1))
		if err := loopholes.MarkLoopholeReminded(ctx, c.ID); err != nil {
			log.Printf("loopholes: %v", err)
		}
	}
}
