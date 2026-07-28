package httpapi

import (
	"os"
	"strings"
	"testing"
)

func TestGovernanceLifecycleHandlersDoNotAccessDatabaseDirectly(t *testing.T) {
	source, err := os.ReadFile("governance_handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"s.DB.", ".Query(", ".QueryRow(", ".Exec(", ".Begin(",
		"SELECT ", "INSERT ", "UPDATE ", "DELETE ",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("governance lifecycle handler contains database access %q", forbidden)
		}
	}
}
