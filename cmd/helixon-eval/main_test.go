package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout to a buffer for the duration of
// fn, then restores the original. Returns the captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// TestListCmd_AllRubricsAndSuites verifies the `list` subcommand
// prints all 5 rubrics and all suites in rubric.AllSuites (TDD: v18667-1
// coverage lift; closes listCmd 0% -> GREEN).
func TestListCmd_AllRubricsAndSuites(t *testing.T) {
	cmd := listCmd()
	out := captureStdout(t, func() {
		cmd.SetOut(io.Discard) // silence cobra's own writer
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("listCmd.Execute: %v", err)
		}
	})

	for _, want := range []string{
		"# Rubrics",
		"# Suites",
		"reliability",
		"observability",
		"security",
		"test_coverage",
		"task_completion",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n---\n%s", want, out)
		}
	}
}

// TestListCmd_FilterByRubric verifies the --rubric filter narrows the
// suites listed.
func TestListCmd_FilterByRubric(t *testing.T) {
	cmd := listCmd()
	out := captureStdout(t, func() {
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"--rubric", "reliability"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("listCmd.Execute: %v", err)
		}
	})
	if !strings.Contains(out, "# Suites") {
		t.Fatalf("expected # Suites header, got: %s", out)
	}
	if !strings.Contains(out, "reliability") {
		t.Errorf("filter --rubric reliability should include reliability suite, got: %s", out)
	}
}

// TestRunCmd_RequiresSuiteFlag verifies `run` fails without --suite.
func TestRunCmd_RequiresSuiteFlag(t *testing.T) {
	cmd := runCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	var runErr error
	captureStdout(t, func() {
		runErr = cmd.Execute()
	})
	if runErr == nil {
		t.Fatalf("runCmd.Execute without --suite should fail")
	}
	if !strings.Contains(runErr.Error(), "--suite required") {
		t.Errorf("expected --suite required error, got: %v", runErr)
	}
}

// TestRunCmd_RejectsUnknownSuite verifies `run` fails on unknown suite name.
func TestRunCmd_RejectsUnknownSuite(t *testing.T) {
	cmd := runCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--suite", "nonexistent-suite-xyz"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runCmd.Execute with unknown suite should fail")
	}
	if !strings.Contains(err.Error(), "unknown suite") {
		t.Errorf("expected unknown suite error, got: %v", err)
	}
}

// TestRunCmd_HappyPath_RunsReliabilitySuite verifies `run --suite reliability`
// executes the suite successfully. Output file is redirected to a
// temp HOME so we don't pollute the home directory.
func TestRunCmd_HappyPath_RunsReliabilitySuite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cmd := runCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	out := captureStdout(t, func() {
		cmd.SetArgs([]string{"--suite", "reliability"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("runCmd.Execute: %v", err)
		}
	})
	if !strings.Contains(out, "\"name\":") {
		t.Errorf("expected JSON output with name field, got: %s", out)
	}
	if !strings.Contains(out, "\"total_cases\":") {
		t.Errorf("expected JSON output with total_cases field, got: %s", out)
	}
}
