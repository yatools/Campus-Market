package httpapi

import "testing"

func TestObserveManualRedactionKeepsRawAndMasksPublicBody(t *testing.T) {
	marked := "昨晚看到==张三==在图书馆，电话 138-1234-5678。"
	raw := stripObserveRedactions(marked)
	if raw != "昨晚看到张三在图书馆，电话 138-1234-5678。" {
		t.Fatalf("unexpected raw body: %q", raw)
	}
	masked := maskObserve(marked)
	if masked != "昨晚看到▓▓▓▓▓▓在图书馆，电话 1**********。" {
		t.Fatalf("unexpected masked body: %q", masked)
	}
}
