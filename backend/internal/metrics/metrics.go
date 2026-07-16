package metrics

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Registry is deliberately small: it exposes the operational signals required
// by the service without making application code depend on a third-party SDK.
type Registry struct {
	mu        sync.Mutex
	counters  map[string]uint64
	durations map[string]duration
	gauges    map[string]float64
}

type duration struct {
	count uint64
	sum   float64
}

var Default = New()

func New() *Registry {
	return &Registry{counters: map[string]uint64{}, durations: map[string]duration{}, gauges: map[string]float64{}}
}

func (r *Registry) Inc(name string) {
	r.mu.Lock()
	r.counters[name]++
	r.mu.Unlock()
}

func (r *Registry) Observe(name string, elapsed time.Duration) {
	r.mu.Lock()
	value := r.durations[name]
	value.count++
	value.sum += elapsed.Seconds()
	r.durations[name] = value
	r.mu.Unlock()
}

func (r *Registry) Set(name string, value float64) {
	r.mu.Lock()
	r.gauges[name] = value
	r.mu.Unlock()
}

func (r *Registry) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	route = strings.ReplaceAll(route, "\\", "_")
	key := fmt.Sprintf("http_requests_total{method=\"%s\",route=\"%s\",status=\"%d\"}", method, route, status)
	r.Inc(key)
	r.Observe(fmt.Sprintf("http_request_duration_seconds{method=\"%s\",route=\"%s\"}", method, route), elapsed)
}

func (r *Registry) ServeHTTP(pool *pgxpool.Pool, w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	counterKeys := make([]string, 0, len(r.counters))
	for key := range r.counters {
		counterKeys = append(counterKeys, key)
	}
	sort.Strings(counterKeys)
	durationKeys := make([]string, 0, len(r.durations))
	for key := range r.durations {
		durationKeys = append(durationKeys, key)
	}
	sort.Strings(durationKeys)
	gaugeKeys := make([]string, 0, len(r.gauges))
	for key := range r.gauges {
		gaugeKeys = append(gaugeKeys, key)
	}
	sort.Strings(gaugeKeys)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for _, key := range counterKeys {
		fmt.Fprintf(w, "%s %d\n", key, r.counters[key])
	}
	for _, key := range durationKeys {
		value := r.durations[key]
		fmt.Fprintf(w, "%s %.6f\n%s %d\n", withSuffix(key, "_sum"), value.sum, withSuffix(key, "_count"), value.count)
	}
	for _, key := range gaugeKeys {
		fmt.Fprintf(w, "%s %.6f\n", key, r.gauges[key])
	}
	r.mu.Unlock()
	if pool == nil {
		return
	}
	stats := pool.Stat()
	fmt.Fprintf(w, "database_pool_acquired_connections %d\n", stats.AcquiredConns())
	fmt.Fprintf(w, "database_pool_idle_connections %d\n", stats.IdleConns())
	fmt.Fprintf(w, "database_pool_total_connections %d\n", stats.TotalConns())
	ctx, cancel := context.WithTimeout(request.Context(), 500*time.Millisecond)
	defer cancel()
	var pending, processing, failed int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='failed') FROM email_outbox`).Scan(&pending, &processing, &failed); err == nil {
		fmt.Fprintf(w, "email_outbox_messages{status=\"pending\"} %d\n", pending)
		fmt.Fprintf(w, "email_outbox_messages{status=\"processing\"} %d\n", processing)
		fmt.Fprintf(w, "email_outbox_messages{status=\"failed\"} %d\n", failed)
	}
	var backupTimestamp float64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(extract(epoch FROM max(finished_at)),0) FROM backup_jobs WHERE status='ready'`).Scan(&backupTimestamp); err == nil {
		fmt.Fprintf(w, "backup_last_success_timestamp_seconds %.0f\n", backupTimestamp)
	}
}

func withSuffix(name, suffix string) string {
	if index := strings.IndexByte(name, '{'); index >= 0 {
		return name[:index] + suffix + name[index:]
	}
	return name + suffix
}
