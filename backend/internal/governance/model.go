package governance

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("governance record not found")

type RuleError struct {
	Code string
}

func (e *RuleError) Error() string { return e.Code }

func rule(code string) error { return &RuleError{Code: code} }

type Actor struct {
	ID   int64
	Role string
}

func (a Actor) CanModerate() bool {
	return a.Role == "moderator" || a.Role == "admin"
}

func (a Actor) IsAdmin() bool {
	return a.Role == "admin"
}

type Account struct {
	ID             int64
	Email          *string
	PasswordHash   string
	Nickname       string
	Alias          string
	CampusIdentity string
	Role           string
	Status         string
	Credit         int
	XP             int
	AvatarPath     *string
	DMStrangerOff  bool
	HideOnline     bool
	VerifiedAt     time.Time
	CreatedAt      time.Time
}

type AccountPatch struct {
	Role           *string
	CampusIdentity *string
	Status         *string
	Credit         *int
	Reason         string
}

type ModerationCase struct {
	ID       int64
	EntityID int64
	Status   string
	Decision string
}

type Entity struct {
	ID                int64
	Type              string
	OwnerID           int64
	PublicationStatus string
}

type ModerationCommand struct {
	Decision   string
	Note       string
	Respondent *int64
	RequestID  string
}

type ModerationResult struct {
	ID       int64
	Status   string
	Decision string
}

type PenaltyCommand struct {
	UserID    int64
	Violation string
	Result    string
	Rule      string
	Delta     int
	RequestID string
}

type PenaltyResult struct {
	ID     int64
	Credit int
}

type AppealResult struct {
	ID     int64
	Status string
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
	Reason    string
	Before    any
	After     any
	RequestID string
}
