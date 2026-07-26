package marketpolicy

const (
	CancelDenied = iota
	CancelAllowed
	CancelNeedsDispute
)

func ListingEditable(status string) bool { return status == "available" }

// ListingCancellable mirrors what cancelMarketListing actually permits. It used to also
// return true for "reserved" while the handler rejected that case a few lines later with
// ACTIVE_TRANSACTION, so the policy package — the thing that exists to be unit-tested —
// described a transition the service never allowed.
func ListingCancellable(status string) bool { return status == "available" }

func ListingRequestable(tradeStatus, publicationStatus, moderationStatus string) bool {
	return tradeStatus == "available" && publicationStatus == "published" && moderationStatus == "approved"
}

func RequestEndable(status string) bool { return status == "requested" }

func Cancellation(status string, buyerConfirmed, sellerConfirmed bool) int {
	switch status {
	case "requested":
		return CancelAllowed
	case "reserved":
		if buyerConfirmed || sellerConfirmed {
			return CancelNeedsDispute
		}
		return CancelAllowed
	default:
		return CancelDenied
	}
}

// Confirmable is strictly about the reserved state. Treating the terminal "completed" as
// confirmable made the guard depend entirely on an early return in the handler: remove
// that return and a completed transaction would be re-completed, rewriting completed_at
// and the listing's trade status. The handler still answers a repeat confirm idempotently;
// that belongs there, not in the state machine.
func Confirmable(status string) bool { return status == "reserved" }

func Disputable(status string) bool { return status == "reserved" }

func Reviewable(status string) bool { return status == "completed" }

func DisputeDecidable(disputeStatus, transactionStatus string) bool {
	return disputeStatus == "pending" && transactionStatus == "disputed"
}
