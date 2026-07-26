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

// knownMethods bounds the cardinality of the method label. Go's HTTP server accepts
// any RFC 7230 token as a method, so an unauthenticated client could otherwise mint an
// unbounded number of label values and grow the (never-evicted) metric maps until the
// process is OOM-killed. Anything unexpected collapses into a single "other" series.
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodOptions: true, http.MethodConnect: true, http.MethodTrace: true,
}

func normalizeMethod(method string) string {
	if knownMethods[method] {
		return method
	}
	return "other"
}

func (r *Registry) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	// Escape label values per the Prometheus text exposition format. Without this a
	// route or method containing a double quote or newline would emit a malformed
	// exposition line and every subsequent /metrics scrape would fail to parse.
	method = escapeLabel(normalizeMethod(method))
	route = escapeLabel(route)
	key := fmt.Sprintf("http_requests_total{method=\"%s\",route=\"%s\",status=\"%d\"}", method, route, status)
	r.Inc(key)
	r.Observe(fmt.Sprintf("http_request_duration_seconds{method=\"%s\",route=\"%s\"}", method, route), elapsed)
}

func escapeLabel(value string) string {
	return strings.NewReplacer("\\", `\\`, "\"", `\"`, "\n", `\n`, "\r", `\r`).Replace(value)
}

// snapshot copies the registry under the lock so the (potentially slow, back-pressured)
// response write happens without holding it. Writing to the socket while holding r.mu
// would let one slow scraper block every in-flight request that records a metric.
func (r *Registry) snapshot() (map[string]uint64, map[string]duration, map[string]float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	counters := make(map[string]uint64, len(r.counters))
	for key, value := range r.counters {
		counters[key] = value
	}
	durations := make(map[string]duration, len(r.durations))
	for key, value := range r.durations {
		durations[key] = value
	}
	gauges := make(map[string]float64, len(r.gauges))
	for key, value := range r.gauges {
		gauges[key] = value
	}
	return counters, durations, gauges
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (r *Registry) ServeHTTP(pool *pgxpool.Pool, w http.ResponseWriter, request *http.Request) {
	counters, durations, gauges := r.snapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for _, key := range sortedKeys(counters) {
		fmt.Fprintf(w, "%s %d\n", key, counters[key])
	}
	for _, key := range sortedKeys(durations) {
		value := durations[key]
		fmt.Fprintf(w, "%s %.6f\n%s %d\n", withSuffix(key, "_sum"), value.sum, withSuffix(key, "_count"), value.count)
	}
	for _, key := range sortedKeys(gauges) {
		fmt.Fprintf(w, "%s %.6f\n", key, gauges[key])
	}
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
	// The worker runs as a separate process without an HTTP listener, so its in-process
	// counters are never scraped. Export the heartbeat it persists to PostgreSQL here so
	// alerting on a dead worker is actually possible.
	if rows, err := pool.Query(ctx, `SELECT worker_name,COALESCE(extract(epoch FROM max(last_success_at)),0),COALESCE(extract(epoch FROM max(last_seen_at)),0) FROM worker_heartbeats GROUP BY worker_name`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var success, seen float64
			if err := rows.Scan(&name, &success, &seen); err != nil {
				break
			}
			label := escapeLabel(name)
			fmt.Fprintf(w, "worker_heartbeat_last_success_timestamp_seconds{worker=\"%s\"} %.0f\n", label, success)
			fmt.Fprintf(w, "worker_heartbeat_last_seen_timestamp_seconds{worker=\"%s\"} %.0f\n", label, seen)
		}
	}
}

func withSuffix(name, suffix string) string {
	if index := strings.IndexByte(name, '{'); index >= 0 {
		return name[:index] + suffix + name[index:]
	}
	return name + suffix
}
