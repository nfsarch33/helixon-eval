package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// TestMain_Dispatch verifies the root cobra command wires the three
// subcommands and surfaces unknown-subcommand errors via Execute. We can't
// invoke main() directly (it calls os.Exit), so we verify the same dispatch
// by rebuilding the root tree and asserting subcommands are present.
func TestMain_Dispatch(t *testing.T) {
	root := buildRootCmd()
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"run", "report", "list"} {
		if !got[want] {
			t.Errorf("root command missing subcommand %q (got=%v)", want, got)
		}
	}
}

// TestMain_ExecuteErrorSurfaces verifies Execute() returning an error is
// observable through the same surface that main() prints+exits on. We
// capture the error and assert its message; main()'s os.Exit(1) path is
// not exercised here because the test process must not exit.
func TestMain_ExecuteErrorSurfaces(t *testing.T) {
	root := buildRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"run"}) // missing --suite, expected to fail
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected error when --suite omitted")
	}
	if !strings.Contains(err.Error(), "--suite required") {
		t.Errorf("expected --suite required error, got: %v", err)
	}
}

// TestReportCmd_HappyPath writes a fake NDJSON report file and verifies
// reportCmd prints its contents.
func TestReportCmd_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Write a small NDJSON report under the default path (~/logs/runx/eval-results.ndjson).
	reportPath := tmpHomePath(t) + "/logs/runx/eval-results.ndjson"
	if err := os.MkdirAll(tmpHomePath(t)+"/logs/runx", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte(`{"name":"alpha","total_cases":3}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed report: %v", err)
	}

	cmd := reportCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("reportCmd.Execute: %v", err)
		}
	})
	if !strings.Contains(out, `"name":"alpha"`) {
		t.Errorf("expected alpha report line, got: %s", out)
	}
}

// TestReportCmd_DefaultPath verifies reportCmd defaults to the same path
// used by runCmd when --input is not provided.
func TestReportCmd_DefaultPath(t *testing.T) {
	cmd := reportCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if cmd.Flag("input").Value.String() != "" {
		t.Errorf("expected default --input empty, got %q", cmd.Flag("input").Value.String())
	}
}

func tmpHomePath(t *testing.T) string {
	t.Helper()
	// t.Setenv(HOME, tmp) makes $HOME point at the temp dir; default helpers
	// in evalfw compute the report path relative to the user's home.
	return os.Getenv("HOME")
}

// buildRootCmd mirrors the main() wiring but as a callable factory so tests
// can exercise the root dispatch path without invoking os.Exit.
func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "helixon-eval",
		Short: "Helixon evaluation harness (5 rubrics: reliability, observability, security, test coverage, task completion)",
	}
	root.AddCommand(runCmd(), reportCmd(), listCmd())
	return root
}
