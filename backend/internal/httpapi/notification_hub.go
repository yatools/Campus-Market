package httpapi

import (
	"context"
	"log/slog"
	"strconv"
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
	return channel, func() {
		h.mu.Lock()
		delete(h.subscribers[userID], channel)
		h.connections--
		operationalmetrics.Default.Set("sse_connections", float64(h.connections))
		if len(h.subscribers[userID]) == 0 {
			delete(h.subscribers, userID)
		}
		h.mu.Unlock()
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

func (h *notificationHub) listen() {
	for {
		if err := h.listenUntilError(); err != nil {
			slog.Warn("notification_listener_disconnected", "error", err)
		}
		time.Sleep(time.Second)
	}
}

func (h *notificationHub) listenUntilError() error {
	conn, err := h.pool.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(context.Background(), "LISTEN wutong_notifications"); err != nil {
		return err
	}
	for {
		notification, err := conn.Conn().WaitForNotification(context.Background())
		if err != nil {
			return err
		}
		userID, err := strconv.ParseInt(notification.Payload, 10, 64)
		if err == nil {
			h.publish(userID)
		}
	}
}
