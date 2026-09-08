// Package ingest contains the collector-to-core delivery client.
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/store"
)

type Client struct {
	BaseURL    string
	Token      string
	DeviceID   string
	HTTPClient *http.Client
}

func (c Client) Send(ctx context.Context, batch model.Batch) (store.Receipt, error) {
	if c.BaseURL == "" || c.Token == "" {
		return store.Receipt{}, errors.New("ingest base URL and token are required")
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return store.Receipt{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/ingest/batches", bytes.NewReader(payload))
	if err != nil {
		return store.Receipt{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	if c.DeviceID != "" {
		if batch.DeviceID != c.DeviceID {
			return store.Receipt{}, errors.New("paired device does not match queued batch")
		}
		request.Header.Set("X-Telemetry-Device-ID", c.DeviceID)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	// Credentials must never follow redirects to another endpoint.
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		return store.Receipt{}, fmt.Errorf("deliver batch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return store.Receipt{}, fmt.Errorf("core rejected batch: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var receipt store.Receipt
	if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
		return store.Receipt{}, fmt.Errorf("decode core receipt: %w", err)
	}
	if receipt.BatchID != batch.ID {
		return store.Receipt{}, errors.New("core receipt batch id does not match request")
	}
	return receipt, nil
}
