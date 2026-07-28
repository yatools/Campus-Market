package httpapi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTeamLifecycleHandlersDoNotContainSQL(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "team_handlers.go"))
	if err != nil {
		t.Fatal(err)
	}
	upper := strings.ToUpper(string(source))
	for _, statement := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", ".QUERY(", ".QUERYROW(", ".EXEC(", ".BEGIN("} {
		if strings.Contains(upper, statement) {
			t.Fatalf("team lifecycle HTTP adapter contains database operation %q", statement)
		}
	}
}
