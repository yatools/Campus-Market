package marketpolicy

import "testing"

func TestListingTransitions(t *testing.T) {
	if !ListingEditable("available") || ListingEditable("reserved") {
		t.Fatal("listing edit policy is invalid")
	}
	if !ListingCancellable("available") {
		t.Fatal("available listing should be cancellable")
	}
	for _, status := range []string{"reserved", "completed", "cancelled", "unknown"} {
		if ListingCancellable(status) {
			t.Fatalf("%s listing should not be cancellable", status)
		}
	}
	if !ListingRequestable("available", "published", "approved") {
		t.Fatal("published available listing should be requestable")
	}
	for _, state := range [][3]string{
		{"reserved", "published", "approved"},
		{"available", "hidden", "approved"},
		{"available", "published", "pending"},
	} {
		if ListingRequestable(state[0], state[1], state[2]) {
			t.Fatalf("listing state %v should not be requestable", state)
		}
	}
}

func TestTransactionTransitions(t *testing.T) {
	if !RequestEndable("requested") || RequestEndable("reserved") {
		t.Fatal("request end policy is invalid")
	}
	if Cancellation("requested", false, false) != CancelAllowed ||
		Cancellation("reserved", false, false) != CancelAllowed ||
		Cancellation("reserved", true, false) != CancelNeedsDispute ||
		Cancellation("reserved", false, true) != CancelNeedsDispute ||
		Cancellation("completed", false, false) != CancelDenied {
		t.Fatal("cancellation policy is invalid")
	}
	if !Confirmable("reserved") || Confirmable("completed") || Confirmable("requested") {
		t.Fatal("confirmation policy is invalid")
	}
	if !Disputable("reserved") || Disputable("completed") {
		t.Fatal("dispute policy is invalid")
	}
	if !Reviewable("completed") || Reviewable("reserved") {
		t.Fatal("review policy is invalid")
	}
	if !DisputeDecidable("pending", "disputed") ||
		DisputeDecidable("resolved", "disputed") ||
		DisputeDecidable("pending", "reserved") {
		t.Fatal("dispute decision policy is invalid")
	}
}
