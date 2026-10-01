// Package mcpapi exposes bounded Telemetry read tools through MCP.
package mcpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/store"
)

type QueryObservationsInput struct {
	SourceInstanceKey string `json:"source_instance_key,omitempty" jsonschema:"source instance to query"`
	From              string `json:"from,omitempty" jsonschema:"inclusive RFC3339 start time"`
	To                string `json:"to,omitempty" jsonschema:"exclusive RFC3339 end time"`
	Limit             int    `json:"limit,omitempty" jsonschema:"maximum records from 1 through 1000"`
}

type QueryObservationsOutput struct {
	Observations []ObservationOutput `json:"observations"`
}

// ObservationOutput uses JSON values at the MCP boundary. json.RawMessage is
// represented as bytes in Go schema inference, which incorrectly advertises a
// payload object as an array to MCP clients.
type ObservationOutput struct {
	SourceInstanceKey string     `json:"source_instance_key"`
	SourceKind        string     `json:"source_kind"`
	DeviceID          string     `json:"device_id"`
	SourceRecordKey   string     `json:"source_record_key"`
	Kind              string     `json:"kind"`
	OccurredAt        time.Time  `json:"occurred_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	CollectedAt       time.Time  `json:"collected_at"`
	RevisionDigest    string     `json:"revision_digest"`
	OriginalPayload   any        `json:"original_payload"`
	Projection        any        `json:"projection"`
}

type ListSourcesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum source summaries from 1 through 1000"`
}

type ListSourcesOutput struct {
	Sources []store.SourceSummary `json:"sources"`
}

type CollectionHealthInput struct {
	Limit   int `json:"limit,omitempty" jsonschema:"maximum source summaries from 1 through 1000"`
	Samples int `json:"samples,omitempty" jsonschema:"latest resource observations per device from 1 through 72"`
}

type CollectionHealthOutput struct {
	CheckedAt        time.Time                      `json:"checked_at"`
	Sources          []store.SourceSummary          `json:"sources"`
	SourcesTruncated bool                           `json:"sources_truncated"`
	Devices          []CollectionHealthDeviceOutput `json:"devices"`
}

type CollectionHealthDeviceOutput struct {
	DeviceID        string              `json:"device_id"`
	LastReceivedAt  *time.Time          `json:"last_received_at,omitempty"`
	ResourceHistory []ObservationOutput `json:"resource_history"`
}

// New returns an authenticated stateless MCP handler. Tool handlers receive
// only the repository interface, never an unrestricted SQL connection.
//
// publicHosts are the server's own public host names (such as its Tailscale
// name). Requests that reach the loopback listener through a proxy may carry
// them; any other host on loopback is refused, as DNS rebinding protection.
func New(repository store.Repository, readToken string, publicHosts ...string) (http.Handler, error) {
	if repository == nil || readToken == "" {
		return nil, errors.New("repository and read token are required")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "telemetry", Title: "Telemetry", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_observations",
		Description: "Read a bounded, provenance-preserving window of current effective telemetry observations.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input QueryObservationsInput) (*mcp.CallToolResult, QueryObservationsOutput, error) {
		query, err := parseQuery(input)
		if err != nil {
			return nil, QueryObservationsOutput{}, err
		}
		observations, err := repository.QueryObservations(ctx, query)
		if err != nil {
			return nil, QueryObservationsOutput{}, err
		}
		output := make([]ObservationOutput, 0, len(observations))
		for _, observation := range observations {
			value, err := observationOutput(observation)
			if err != nil {
				return nil, QueryObservationsOutput{}, err
			}
			output = append(output, value)
		}
		return nil, QueryObservationsOutput{Observations: output}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sources",
		Description: "List bounded source coverage summaries, including observation counts and collection times.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ListSourcesInput) (*mcp.CallToolResult, ListSourcesOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 100
		}
		if limit < 1 || limit > 1000 {
			return nil, ListSourcesOutput{}, errors.New("limit must be between 1 and 1000")
		}
		sources, err := repository.ListSources(ctx, limit)
		if err != nil {
			return nil, ListSourcesOutput{}, err
		}
		return nil, ListSourcesOutput{Sources: sources}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "collection_health",
		Description: "Read bounded source coverage and recent collector resource status observations. This reports collection evidence and limits, not a health diagnosis.",
	}, collectionHealthTool(repository))
	addActivityTools(server, repository, time.Now)
	addChangeTools(server, repository)
	addPlanningTools(server, repository, time.Now)
	addFollowUpTools(server, repository, time.Now)
	addRuleTools(server, repository)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
		// tokenHandler applies the same protection with the public hosts allowed.
		DisableLocalhostProtection: true,
	})
	allowed := map[string]bool{}
	for _, host := range publicHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = true
		}
	}
	return tokenHandler{next: handler, token: readToken, publicHosts: allowed}, nil
}

func collectionHealthTool(repository store.Repository) func(context.Context, *mcp.CallToolRequest, CollectionHealthInput) (*mcp.CallToolResult, CollectionHealthOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CollectionHealthInput) (*mcp.CallToolResult, CollectionHealthOutput, error) {
		query, err := parseCollectionHealth(input)
		if err != nil {
			return nil, CollectionHealthOutput{}, err
		}
		health, err := repository.CollectionHealth(ctx, query)
		if err != nil {
			return nil, CollectionHealthOutput{}, err
		}
		output, err := collectionHealthOutput(health)
		if err != nil {
			return nil, CollectionHealthOutput{}, err
		}
		return nil, output, nil
	}
}

func observationOutput(input store.ObservationView) (ObservationOutput, error) {
	output := ObservationOutput{
		SourceInstanceKey: input.SourceInstanceKey,
		SourceKind:        input.SourceKind,
		DeviceID:          input.DeviceID,
		SourceRecordKey:   input.SourceRecordKey,
		Kind:              input.Kind,
		OccurredAt:        input.OccurredAt,
		EndedAt:           input.EndedAt,
		CollectedAt:       input.CollectedAt,
		RevisionDigest:    input.RevisionDigest,
	}
	if err := json.Unmarshal(input.OriginalPayload, &output.OriginalPayload); err != nil {
		return ObservationOutput{}, fmt.Errorf("decode original payload for MCP: %w", err)
	}
	if err := json.Unmarshal(input.Projection, &output.Projection); err != nil {
		return ObservationOutput{}, fmt.Errorf("decode projection for MCP: %w", err)
	}
	return output, nil
}

func collectionHealthOutput(input store.CollectionHealth) (CollectionHealthOutput, error) {
	output := CollectionHealthOutput{
		CheckedAt:        input.CheckedAt,
		Sources:          input.Sources,
		SourcesTruncated: input.SourcesTruncated,
		Devices:          make([]CollectionHealthDeviceOutput, 0, len(input.Devices)),
	}
	for _, device := range input.Devices {
		converted := CollectionHealthDeviceOutput{
			DeviceID:        device.DeviceID,
			LastReceivedAt:  device.LastReceivedAt,
			ResourceHistory: make([]ObservationOutput, 0, len(device.ResourceHistory)),
		}
		for _, resource := range device.ResourceHistory {
			value, err := observationOutput(resource)
			if err != nil {
				return CollectionHealthOutput{}, err
			}
			converted.ResourceHistory = append(converted.ResourceHistory, value)
		}
		output.Devices = append(output.Devices, converted)
	}
	return output, nil
}

func parseCollectionHealth(input CollectionHealthInput) (store.CollectionHealthQuery, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return store.CollectionHealthQuery{}, errors.New("limit must be between 1 and 1000")
	}
	samples := input.Samples
	if samples == 0 {
		samples = 24
	}
	if samples < 1 || samples > 72 {
		return store.CollectionHealthQuery{}, errors.New("samples must be between 1 and 72")
	}
	return store.CollectionHealthQuery{SourceLimit: limit, ResourceSamples: samples}, nil
}

type tokenHandler struct {
	next        http.Handler
	token       string
	publicHosts map[string]bool
}

func isLoopback(host string) bool {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

func (h tokenHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// DNS rebinding protection: a request reaching the loopback listener must
	// name loopback or this server's own public host.
	if local, ok := request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && local != nil {
		listener, _, _ := net.SplitHostPort(local.String())
		host := request.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		if isLoopback(listener) && !isLoopback(host) && !h.publicHosts[strings.ToLower(host)] {
			http.Error(writer, `{"error":"invalid Host header"}`, http.StatusForbidden)
			return
		}
	}
	value := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if value == request.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(value), []byte(h.token)) != 1 {
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, `{"error":"valid read credentials are required"}`, http.StatusUnauthorized)
		return
	}
	h.next.ServeHTTP(writer, request)
}

func parseQuery(input QueryObservationsInput) (store.Query, error) {
	query := store.Query{SourceInstanceKey: input.SourceInstanceKey, Limit: input.Limit}
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 1 || query.Limit > 1000 {
		return query, errors.New("limit must be between 1 and 1000")
	}
	var err error
	if input.From != "" {
		query.From, err = mcpTime(input.From)
		if err != nil {
			return query, fmt.Errorf("from: %w", err)
		}
	}
	if input.To != "" {
		query.To, err = mcpTime(input.To)
		if err != nil {
			return query, fmt.Errorf("to: %w", err)
		}
	}
	if query.From != nil && query.To != nil && !query.From.Before(*query.To) {
		return query, errors.New("from must be before to")
	}
	return query, nil
}

func mcpTime(value string) (*time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("must be RFC3339")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
