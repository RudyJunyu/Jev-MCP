package hub

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Metrics contains process-local counters. The gateway is stateless, so these
// metrics intentionally reset when the container restarts.
type Metrics struct {
	requests      atomic.Uint64
	errors        atomic.Uint64
	status2xx     atomic.Uint64
	status4xx     atomic.Uint64
	status5xx     atomic.Uint64
	durationNanos atomic.Uint64
	toolCalls     [4]atomic.Uint64
	toolErrors    [4]atomic.Uint64
	toolNanos     [4]atomic.Uint64
}

var toolNames = [...]string{"evaluate", "choice", "score", "noul"}

func NewMetrics() *Metrics { return new(Metrics) }

func (m *Metrics) observe(status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.requests.Add(1)
	m.durationNanos.Add(uint64(elapsed))
	switch {
	case status >= 200 && status < 300:
		m.status2xx.Add(1)
	case status >= 400 && status < 500:
		m.status4xx.Add(1)
		m.errors.Add(1)
	case status >= 500:
		m.status5xx.Add(1)
		m.errors.Add(1)
	}
}

func toolIndex(kind string) int {
	for i, name := range toolNames {
		if kind == name {
			return i
		}
	}
	return -1
}

func (m *Metrics) observeTool(kind string, failed bool, elapsed time.Duration) {
	if m == nil {
		return
	}
	index := toolIndex(kind)
	if index < 0 {
		return
	}
	m.toolCalls[index].Add(1)
	m.toolNanos[index].Add(uint64(elapsed))
	if failed {
		m.toolErrors[index].Add(1)
	}
}

// ServeHTTP exposes Prometheus text format without including request paths,
// headers, or token-derived values in metric labels.
func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	requests := m.requests.Load()
	seconds := float64(m.durationNanos.Load()) / float64(time.Second)
	fmt.Fprintf(w, "# HELP jev_http_requests_total Total HTTP requests handled by Jev MCP Hub.\n# TYPE jev_http_requests_total counter\njev_http_requests_total %d\n", requests)
	fmt.Fprintf(w, "# HELP jev_http_errors_total Total HTTP responses with 4xx or 5xx status.\n# TYPE jev_http_errors_total counter\njev_http_errors_total %d\n", m.errors.Load())
	fmt.Fprintf(w, "# HELP jev_http_requests_by_status_total HTTP responses grouped by status class.\n# TYPE jev_http_requests_by_status_total counter\njev_http_requests_by_status_total{class=\"2xx\"} %d\njev_http_requests_by_status_total{class=\"4xx\"} %d\njev_http_requests_by_status_total{class=\"5xx\"} %d\n", m.status2xx.Load(), m.status4xx.Load(), m.status5xx.Load())
	fmt.Fprintf(w, "# HELP jev_http_request_duration_seconds_sum Sum of HTTP request durations in seconds.\n# TYPE jev_http_request_duration_seconds_sum counter\njev_http_request_duration_seconds_sum %s\n", strconv.FormatFloat(seconds, 'f', 9, 64))
	fmt.Fprintf(w, "# HELP jev_http_request_duration_seconds_count Number of observed HTTP request durations.\n# TYPE jev_http_request_duration_seconds_count counter\njev_http_request_duration_seconds_count %d\n", requests)
	fmt.Fprintln(w, "# HELP jev_mcp_tool_calls_total Total MCP tool calls grouped by tool and outcome.")
	fmt.Fprintln(w, "# TYPE jev_mcp_tool_calls_total counter")
	for i, name := range toolNames {
		calls := m.toolCalls[i].Load()
		errors := m.toolErrors[i].Load()
		fmt.Fprintf(w, "jev_mcp_tool_calls_total{tool=%q,outcome=\"success\"} %d\n", name, calls-errors)
		fmt.Fprintf(w, "jev_mcp_tool_calls_total{tool=%q,outcome=\"error\"} %d\n", name, errors)
	}
	fmt.Fprintln(w, "# HELP jev_mcp_tool_duration_seconds MCP tool call duration in seconds.")
	fmt.Fprintln(w, "# TYPE jev_mcp_tool_duration_seconds summary")
	for i, name := range toolNames {
		seconds := float64(m.toolNanos[i].Load()) / float64(time.Second)
		fmt.Fprintf(w, "jev_mcp_tool_duration_seconds_sum{tool=%q} %s\n", name, strconv.FormatFloat(seconds, 'f', 9, 64))
		fmt.Fprintf(w, "jev_mcp_tool_duration_seconds_count{tool=%q} %d\n", name, m.toolCalls[i].Load())
	}
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func withObservability(next http.Handler, logger *slog.Logger, metrics *Metrics) http.Handler {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if metrics == nil {
		metrics = NewMetrics()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if !validRequestID(requestID) {
			requestID = fmt.Sprintf("jev-%d", started.UnixNano())
		}
		w.Header().Set("X-Request-ID", requestID)
		wrapped := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		elapsed := time.Since(started)
		// A scrape should not increase the counters it is reading.
		if r.URL.Path != "/metrics" {
			metrics.observe(status, elapsed)
		}
		attrs := []any{"request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", status, "bytes", wrapped.bytes, "duration_ms", elapsed.Seconds() * 1000}
		switch {
		case status >= 500:
			logger.Error("http_request", attrs...)
		case status >= 400:
			logger.Warn("http_request", attrs...)
		default:
			logger.Info("http_request", attrs...)
		}
	})
}
