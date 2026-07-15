package marketpolicy

import "testing"

func TestListingPolicy(t *testing.T) {
	if !ListingEditable("available") || ListingEditable("reserved") {
		t.Fatal("listing edit policy is invalid")
	}
	for _, status := range []string{"available", "reserved"} {
		if !ListingCancellable(status) {
			t.Fatalf("%s should be cancellable", status)
		}
	}
	for _, status := range []string{"completed", "cancelled", "unknown"} {
		if ListingCancellable(status) {
			t.Fatalf("%s should not be cancellable", status)
		}
	}
	if !ListingRequestable("available", "published", "approved") || ListingRequestable("available", "hidden", "approved") || ListingRequestable("reserved", "published", "approved") || ListingRequestable("available", "published", "hidden") {
		t.Fatal("listing request policy is invalid")
	}
}

func TestTransactionPolicy(t *testing.T) {
	if !RequestAcceptable("requested", "available") || RequestAcceptable("reserved", "available") || RequestAcceptable("requested", "reserved") {
		t.Fatal("accept policy is invalid")
	}
	if !RequestEndable("requested") || RequestEndable("reserved") {
		t.Fatal("request end policy is invalid")
	}
	if Cancellation("requested", false, false) != CancelAllowed || Cancellation("reserved", false, false) != CancelAllowed || Cancellation("reserved", true, false) != CancelNeedsDispute || Cancellation("reserved", false, true) != CancelNeedsDispute || Cancellation("completed", false, false) != CancelDenied {
		t.Fatal("cancellation policy is invalid")
	}
	if !Confirmable("reserved") || !Confirmable("completed") || Confirmable("requested") {
		t.Fatal("confirmation policy is invalid")
	}
	if !Disputable("reserved") || Disputable("completed") || !Reviewable("completed") || Reviewable("reserved") {
		t.Fatal("dispute or review policy is invalid")
	}
	if !DisputeDecidable("pending", "disputed") || DisputeDecidable("resolved", "disputed") || DisputeDecidable("pending", "reserved") {
		t.Fatal("dispute decision policy is invalid")
	}
}
