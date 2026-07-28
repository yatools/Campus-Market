package market

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("market record not found")
	ErrConflict = errors.New("market concurrent conflict")
)

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

type Listing struct {
	ID                int64
	OwnerID           int64
	Title             string
	TradeStatus       string
	PublicationStatus string
	ModerationStatus  string
}

type Transaction struct {
	ID                int64
	ListingID         int64
	SellerID          int64
	BuyerID           int64
	Status            string
	ReservedUntil     *time.Time
	BuyerConfirmedAt  *time.Time
	SellerConfirmedAt *time.Time
}

func (t Transaction) Includes(userID int64) bool {
	return userID == t.BuyerID || userID == t.SellerID
}

type Dispute struct {
	ID            int64
	TransactionID int64
	Status        string
}

type Result struct {
	ListingID     int64
	TransactionID int64
	DisputeID     int64
	ReviewID      int64
	Status        string
	Decision      string
	VisibleAt     time.Time
}

type AuditEntry struct {
	ActorID   int64
	Action    string
	Target    string
	TargetID  int64
	Reason    string
	Before    map[string]any
	After     map[string]any
	RequestID string
}

type Notification struct {
	UserID int64
	Title  string
	Body   string
	Link   string
}
