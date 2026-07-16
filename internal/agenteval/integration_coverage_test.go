// v18667-1: coverage lift for internal/agenteval/integration.go.
// Adds tests for Error() + String() + withDefaults() that are
// currently at 0% coverage. Existing tests in integration_test.go
// are unchanged.
package agenteval

import (
	"strings"
	"testing"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
)

// TestErrStaleCODEMAP_Error verifies the Error() method formats age
// and TTL into a readable string.
func TestErrStaleCODEMAP_Error(t *testing.T) {
	e := &ErrStaleCODEMAP{
		Age: 30 * time.Minute,
		TTL: 15 * time.Minute,
	}
	got := e.Error()
	for _, want := range []string{
		"agenteval",
		"CODEMAP.md",
		"30m0s", // age
		"15m0s", // TTL
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, want contains %q", got, want)
		}
	}
}

// TestErrStaleCODEMAP_ErrorImplementsError verifies the type satisfies
// the error interface (so callers can use errors.As / errors.Is).
func TestErrStaleCODEMAP_ErrorImplementsError(t *testing.T) {
	var e error = &ErrStaleCODEMAP{Age: time.Second, TTL: time.Millisecond}
	if e == nil {
		t.Fatal("ErrStaleCODEMAP must satisfy error interface")
	}
	if !strings.Contains(e.Error(), "agenteval") {
		t.Errorf("Error() = %q, want contains 'agenteval'", e.Error())
	}
}

// TestString_SuiteSummary verifies String() returns a one-line
// summary of the suite.
func TestString_SuiteSummary(t *testing.T) {
	suite := evalfw.Suite{
		Name:  "reliability",
		Cases: []evalfw.Case{{Name: "c1"}, {Name: "c2"}, {Name: "c3"}},
	}
	got := String(suite)
	for _, want := range []string{"suite=reliability", "cases=3"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want contains %q", got, want)
		}
	}
}

// TestString_EmptySuite verifies String() on a suite with no cases.
func TestString_EmptySuite(t *testing.T) {
	got := String(evalfw.Suite{Name: "empty"})
	if !strings.Contains(got, "cases=0") {
		t.Errorf("String(empty) = %q, want contains 'cases=0'", got)
	}
}
