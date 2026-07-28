package team

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("team record not found")

type RuleError struct {
	Code string
}

func (e *RuleError) Error() string { return e.Code }

func rule(code string) error { return &RuleError{Code: code} }

type Actor struct {
	ID       int64
	Role     string
	Nickname string
}

func (a Actor) CanModerate() bool {
	return a.Role == "moderator" || a.Role == "admin"
}

type Team struct {
	ID                int64
	OwnerID           int64
	Game              string
	Mode              string
	Capacity          int
	Status            string
	PublicationStatus string
}

type Run struct {
	ID     int64
	TeamID int64
	Starts time.Time
	Status string
}

type Membership struct {
	ID       int64
	UserID   int64
	Role     string
	Status   string
	Channels string
}

type RunMember struct {
	ID        int64
	Status    string
	CheckedAt *time.Time
	ExcusedAt *time.Time
	Awarded   bool
}

type Result struct {
	TeamID      int64
	RunID       int64
	CreditDelta int
	CheckedAt   *time.Time
	ExcusedAt   *time.Time
}

type Notification struct {
	UserID int64
	Title  string
	Body   string
	Link   string
}

type AuditEntry struct {
	ActorID   int64
	Action    string
	Target    string
	TargetID  int64
	After     map[string]any
	RequestID string
}
