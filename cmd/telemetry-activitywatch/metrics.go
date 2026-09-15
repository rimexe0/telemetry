package main

import (
	"io"
	"net/http"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

// bodyByteCounter measures HTTP payload bytes for one bounded collector pass.
// It deliberately excludes headers and transport overhead, which keeps the
// value tied to source and ingest payload volume rather than a particular
// network implementation.
type bodyByteCounter struct {
	request  atomic.Uint64
	response atomic.Uint64
}

func (c *bodyByteCounter) requestBytes() uint64 { return c.request.Load() }

func (c *bodyByteCounter) responseBytes() uint64 { return c.response.Load() }

type countingTransport struct {
	base  http.RoundTripper
	bytes *bodyByteCounter
}

func (t countingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.bytes != nil && request.Body != nil {
		request.Body = &countingBody{ReadCloser: request.Body, count: &t.bytes.request}
	}
	response, err := base.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil || t.bytes == nil {
		return response, err
	}
	response.Body = &countingBody{ReadCloser: response.Body, count: &t.bytes.response}
	return response, nil
}

type countingBody struct {
	io.ReadCloser
	count *atomic.Uint64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.count != nil {
		b.count.Add(uint64(n))
	}
	return n, err
}

type processUsage struct {
	user   time.Duration
	system time.Duration
	maxRSS int64
}

func readProcessUsage() (processUsage, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return processUsage{}, err
	}
	maxRSS := usage.Maxrss
	// Darwin reports ru_maxrss in bytes while Linux reports KiB. The
	// collector runs on both platforms, so normalize the exported metric.
	if runtime.GOOS == "linux" {
		maxRSS *= 1024
	}
	return processUsage{
		user:   timevalDuration(usage.Utime),
		system: timevalDuration(usage.Stime),
		maxRSS: maxRSS,
	}, nil
}

func timevalDuration(value syscall.Timeval) time.Duration {
	return time.Duration(value.Sec)*time.Second + time.Duration(value.Usec)*time.Microsecond
}

type passMetrics struct {
	WallDurationMS        int64       `json:"wall_duration_ms"`
	CPUUserMS             int64       `json:"cpu_user_ms"`
	CPUSystemMS           int64       `json:"cpu_system_ms"`
	CPUPercent            float64     `json:"cpu_percent"`
	MaxRSSBytes           int64       `json:"max_rss_bytes"`
	HTTPBodyRequestBytes  uint64      `json:"http_body_request_bytes"`
	HTTPBodyResponseBytes uint64      `json:"http_body_response_bytes"`
	Queue                 queue.Stats `json:"queue"`
}

func makePassMetrics(start, end time.Time, before, after processUsage, bytes *bodyByteCounter, stats queue.Stats) passMetrics {
	wall := end.Sub(start)
	user := after.user - before.user
	system := after.system - before.system
	metrics := passMetrics{
		WallDurationMS: end.Sub(start).Milliseconds(),
		CPUUserMS:      user.Milliseconds(),
		CPUSystemMS:    system.Milliseconds(),
		MaxRSSBytes:    after.maxRSS,
		Queue:          stats,
	}
	if wall > 0 {
		metrics.CPUPercent = float64(user+system) / float64(wall) * 100
	}
	if bytes != nil {
		metrics.HTTPBodyRequestBytes = bytes.requestBytes()
		metrics.HTTPBodyResponseBytes = bytes.responseBytes()
	}
	return metrics
}
