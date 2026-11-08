// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type httpMetricKey struct {
	method string
	status int
}
type httpMetrics struct {
	started  time.Time
	mu       sync.Mutex
	requests map[httpMetricKey]uint64
	count    uint64
	duration time.Duration
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{started: time.Now(), requests: make(map[httpMetricKey]uint64)}
}
func instrumentHTTP(metrics *httpMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		if rw.status == 0 {
			rw.status = http.StatusOK
		}
		metrics.mu.Lock()
		metrics.requests[httpMetricKey{metricMethod(r.Method), rw.status}]++
		metrics.count++
		metrics.duration += time.Since(started)
		metrics.mu.Unlock()
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (m *httpMetrics) render() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]httpMetricKey, 0, len(m.requests))
	for key := range m.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method == keys[j].method {
			return keys[i].status < keys[j].status
		}
		return keys[i].method < keys[j].method
	})
	var b strings.Builder
	b.WriteString("# HELP easybotce_http_requests_total Total HTTP requests handled by the Hub.\n# TYPE easybotce_http_requests_total counter\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "easybotce_http_requests_total{method=%q,status=%q} %d\n", key.method, fmt.Sprint(key.status), m.requests[key])
	}
	b.WriteString("# HELP easybotce_http_request_duration_seconds_sum Cumulative HTTP request handling time in seconds.\n# TYPE easybotce_http_request_duration_seconds_sum counter\n")
	fmt.Fprintf(&b, "easybotce_http_request_duration_seconds_sum %.9f\n", m.duration.Seconds())
	b.WriteString("# HELP easybotce_http_request_duration_seconds_count Total HTTP request observations.\n# TYPE easybotce_http_request_duration_seconds_count counter\n")
	fmt.Fprintf(&b, "easybotce_http_request_duration_seconds_count %d\n", m.count)
	fmt.Fprintf(&b, "# HELP easybotce_uptime_seconds Hub process uptime in seconds.\n# TYPE easybotce_uptime_seconds gauge\neasybotce_uptime_seconds %.3f\n", time.Since(m.started).Seconds())
	return []byte(b.String())
}
func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}
