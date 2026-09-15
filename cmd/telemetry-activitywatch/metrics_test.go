package main

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

func TestCountingTransportReportsHTTPBodyBytes(t *testing.T) {
	requestBody := []byte(`{"event":"activity"}`)
	responseBody := []byte(`{"accepted":true}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		got, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, requestBody) {
			t.Fatalf("request body = %q, want %q", got, requestBody)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(responseBody)
	}))
	defer server.Close()

	counter := &bodyByteCounter{}
	client := &http.Client{Transport: countingTransport{base: http.DefaultTransport, bytes: counter}}
	request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got, err := io.ReadAll(response.Body); err != nil || !bytes.Equal(got, responseBody) {
		t.Fatalf("response body = %q, %v", got, err)
	}
	if got := counter.requestBytes(); got != uint64(len(requestBody)) {
		t.Fatalf("request bytes = %d, want %d", got, len(requestBody))
	}
	if got := counter.responseBytes(); got != uint64(len(responseBody)) {
		t.Fatalf("response bytes = %d, want %d", got, len(responseBody))
	}
}

func TestMakePassMetricsUsesBoundedPassDelta(t *testing.T) {
	start := time.Unix(100, 0)
	end := start.Add(2*time.Second + 500*time.Millisecond)
	metrics := makePassMetrics(start, end,
		processUsage{user: 100 * time.Millisecond, system: 50 * time.Millisecond},
		processUsage{user: 350 * time.Millisecond, system: 150 * time.Millisecond, maxRSS: 4096},
		&bodyByteCounter{},
		queue.Stats{Pending: 2, Leased: 1, Acknowledged: 3},
	)
	if metrics.WallDurationMS != 2500 || metrics.CPUUserMS != 250 || metrics.CPUSystemMS != 100 || math.Abs(metrics.CPUPercent-14) > 0.001 {
		t.Fatalf("pass duration metrics = %+v", metrics)
	}
	if metrics.MaxRSSBytes != 4096 || metrics.Queue.Pending != 2 {
		t.Fatalf("resource/queue metrics = %+v", metrics)
	}
}
