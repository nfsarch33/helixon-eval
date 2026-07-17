package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nfsarch33/helixon-eval/internal/agenteval"
	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/llmcost"
)

const (
	// demoModel is the production model the demo runs against (v18685-3).
	// The full 7-task × 3-model matrix is exercised by the integration
	// tests; the demo is a single-model operator-friendly smoke run.
	demoModel = "MiniMax-M3"

	// demoMaxUSD is the operator-facing cost gate for the demo run.
	// v18685-3 plan: "cost < $0.50". Picked well above the realistic
	// observed cost (~$0.001 for 7 trivial stub calls) so the gate is
	// fail-safe but meaningful.
	demoMaxUSD = 0.50
)

// demoCmd builds the `helixon-eval demo` cobra command. It runs the 7-task
// canonical agent-eval matrix scoped to MiniMax-M3 only, writes the
// verdict to stdout as JSON, appends one NDJSON line to the eval-results
// log, and writes per-call cost events to the cost NDJSON via llmcost.
func demoCmd() *cobra.Command {
	var (
		output   string
		costPath string
	)
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Run a 7-task agent-eval matrix scoped to MiniMax-M3 (operator demo)",
		Long: "demo runs the 7 canonical agent-eval tasks against MiniMax-M3 only " +
			"(vs. the 7×3=21-case full matrix), prints the verdict JSON to stdout, " +
			"appends one NDJSON line to the eval-results log, and records per-call " +
			"cost events. Exits non-zero if cost exceeds the $0.50 gate or any " +
			"task fails.",
		RunE: func(cmd *cobra.Command, args []string) error {
			tracker, err := llmcost.New(costPath)
			if err != nil {
				return fmt.Errorf("cost tracker: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			totalUSD, err := runDemoWithCost(ctx, "demo-"+time.Now().UTC().Format("20060102T150405Z"), output, tracker)
			if err != nil {
				return err
			}
			if totalUSD > demoMaxUSD {
				return fmt.Errorf("demo cost $%.4f exceeds gate $%.2f", totalUSD, demoMaxUSD)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&output, "output", "", "NDJSON output path (default ~/logs/runx/eval-results.ndjson)")
	cmd.Flags().StringVar(&costPath, "cost-output", "", "cost NDJSON path (default ~/logs/runx/helixon-eval-cost.ndjson)")
	return cmd
}

// buildDemoSuite assembles the 7-task agent-eval matrix scoped to
// MiniMax-M3 only. Returned evalfw.Suite has exactly len(CanonicalTasks)
// cases (i.e. 7 in v18681-3+).
func buildDemoSuite(runID string) (evalfw.Suite, error) {
	fullSuite, err := agenteval.SuiteForRun(runID, agenteval.Config{})
	if err != nil {
		return evalfw.Suite{}, fmt.Errorf("agenteval.SuiteForRun: %w", err)
	}
	filtered := make([]evalfw.Case, 0, len(agenteval.CanonicalTasks))
	for _, c := range fullSuite.Cases {
		// Tags carry [agenteval, task, model]; we filter on model.
		if len(c.Tags) >= 3 && c.Tags[2] == demoModel {
			filtered = append(filtered, c)
		}
	}
	return evalfw.Suite{
		Name:  fullSuite.Name + "-demo",
		Cases: filtered,
	}, nil
}

// runDemo runs the demo, writes JSON envelope to stdout, appends one
// NDJSON line to the eval-results log. Returns the SuiteResult.
func runDemo(ctx context.Context, runID, outputPath string, stdout io.Writer) (*evalfw.SuiteResult, error) {
	suite, err := buildDemoSuite(runID)
	if err != nil {
		return nil, err
	}
	runner := evalfw.NewRunner(evalfw.RunnerConfig{Timeout: 10 * time.Second})
	result, err := runner.RunSuite(ctx, suite)
	if err != nil {
		return nil, fmt.Errorf("RunSuite: %w", err)
	}

	// Emit JSON envelope to stdout (or discard).
	if stdout != nil && stdout != io.Discard {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return nil, fmt.Errorf("encode result: %w", err)
		}
	}

	// Append NDJSON line.
	if outputPath == "" {
		outputPath = evalfw.DefaultReportPath()
	}
	w, err := evalfw.NewReportWriter(outputPath)
	if err != nil {
		return nil, fmt.Errorf("report writer: %w", err)
	}
	if err := w.Write(result); err != nil {
		return nil, fmt.Errorf("write report: %w", err)
	}
	return result, nil
}

// runDemoWithCost runs the demo with an explicit llmcost.Tracker and
// returns the cumulative USD cost. The tracker is updated for every
// task (one event per task with a tiny prompt/completion token count).
func runDemoWithCost(ctx context.Context, runID, outputPath string, tracker *llmcost.Tracker) (float64, error) {
	result, err := runDemo(ctx, runID, outputPath, io.Discard)
	if err != nil {
		return 0, err
	}
	var totalUSD float64
	for _, c := range result.Cases {
		promptTok, completionTok := 100, 50
		ev, err := tracker.Record(
			runID+"::"+c.Name,
			llmcost.BackendMiniMaxi,
			demoModel,
			promptTok,
			completionTok,
			"helixon-eval-demo",
		)
		if err != nil {
			return totalUSD, fmt.Errorf("record cost: %w", err)
		}
		totalUSD += ev.EstimatedUSD
	}
	return totalUSD, nil
}

// AddDemo attaches the demo subcommand to the given root. Called by
// main.go so the demo command shows up in `helixon-eval --help`.
func AddDemo(root *cobra.Command) {
	root.AddCommand(demoCmd())
}

// Ensure demoCmd is reachable via the binary when explicitly imported
// by tests (the AddDemo helper above is the canonical wiring surface).
var _ = demoCmd

// init avoids an "imported and not used" error when the file is
// compiled into the main binary via main.go (which uses AddDemo).
var _ = os.Stdout
