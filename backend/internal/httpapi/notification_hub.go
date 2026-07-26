package httpapi

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	operationalmetrics "github.com/yatools/wutong-campus-wall/backend/internal/metrics"
)

type notificationHub struct {
	pool        *pgxpool.Pool
	once        sync.Once
	mu          sync.RWMutex
	subscribers map[int64]map[chan struct{}]struct{}
	connections int
}

func newNotificationHub(pool *pgxpool.Pool) *notificationHub {
	return &notificationHub{pool: pool, subscribers: map[int64]map[chan struct{}]struct{}{}}
}

func (h *notificationHub) subscribe(userID int64) (<-chan struct{}, func()) {
	channel := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subscribers[userID] == nil {
		h.subscribers[userID] = map[chan struct{}]struct{}{}
	}
	h.subscribers[userID][channel] = struct{}{}
	h.connections++
	operationalmetrics.Default.Set("sse_connections", float64(h.connections))
	h.mu.Unlock()
	h.once.Do(func() { go h.listen() })
	var release sync.Once
	// sync.Once because callers may invoke the cancel function on more than one path
	// (handler return plus a deferred cleanup); a double call would decrement the gauge
	// twice and drift it negative.
	return channel, func() {
		release.Do(func() {
			h.mu.Lock()
			delete(h.subscribers[userID], channel)
			h.connections--
			operationalmetrics.Default.Set("sse_connections", float64(h.connections))
			if len(h.subscribers[userID]) == 0 {
				delete(h.subscribers, userID)
			}
			h.mu.Unlock()
		})
	}
}

func (h *notificationHub) publish(userID int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for channel := range h.subscribers[userID] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}

// listenLease bounds how long a single LISTEN connection is held before it is released
// and re-acquired. It exists so shutdown can make progress: pgxpool.Close blocks until
// every checked-out connection is returned, and a listener parked forever inside
// WaitForNotification never returns one, which hung the process on SIGTERM until the
// orchestrator escalated to SIGKILL.
const listenLease = 30 * time.Second

func (h *notificationHub) listen() {
	for {
		err := h.listenUntilError()
		if err == nil {
			// Lease elapsed: reconnect immediately. PostgreSQL does not queue NOTIFY for
			// absent listeners, so any pause here is a window in which notifications are
			// lost outright — a one-second sleep every 30 seconds would drop ~3% of them.
			continue
		}
		// The pool is only ever closed on shutdown; stop rather than log once a second.
		if strings.Contains(err.Error(), "closed pool") {
			return
		}
		slog.Warn("notification_listener_disconnected", "error", err)
		time.Sleep(time.Second)
	}
}

func (h *notificationHub) listenUntilError() error {
	ctx, cancel := context.WithTimeout(context.Background(), listenLease)
	defer cancel()
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN wutong_notifications"); err != nil {
		return err
	}
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // lease elapsed: hand the connection back and take a fresh one
			}
			return err
		}
		userID, err := strconv.ParseInt(notification.Payload, 10, 64)
		if err == nil {
			h.publish(userID)
		}
	}
}
