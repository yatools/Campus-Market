package marketpolicy

const (
	CancelDenied = iota
	CancelAllowed
	CancelNeedsDispute
)

func ListingEditable(status string) bool { return status == "available" }

func ListingCancellable(status string) bool { return status == "available" }

func ListingRequestable(tradeStatus, publicationStatus, moderationStatus string) bool {
	return tradeStatus == "available" &&
		publicationStatus == "published" &&
		moderationStatus == "approved"
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

func Confirmable(status string) bool { return status == "reserved" }

func Disputable(status string) bool { return status == "reserved" }

func Reviewable(status string) bool { return status == "completed" }

func DisputeDecidable(disputeStatus, transactionStatus string) bool {
	return disputeStatus == "pending" && transactionStatus == "disputed"
}
