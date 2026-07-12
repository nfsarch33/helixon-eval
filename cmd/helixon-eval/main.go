// Command helixon-eval is the operator entry point for the Helixon
// evaluation harness. It exposes four subcommands:
//
//	helixon-eval run       -- run an evaluation suite and append to NDJSON report
//	helixon-eval report    -- aggregate and print NDJSON report
//	helixon-eval list      -- list available rubrics and suites
//	helixon-eval leaderboard -- aggregate the R6 per-model leaderboard
//
// R6 adds a --provider / --model / --api-key-stdin extension so a
// real LLM backend (minimax/MiniMax-M3, qwen/qwen3.7-plus, qwen/qwen3.7-max)
// can be exercised against the suite. API keys are piped via stdin from
// `op read`; they never appear on argv (no-shell-leak.mdc).
//
// Every Case registered against a Suite implements one of the 12
// dimensions: reliability, observability, security, test coverage,
// task completion, eval_harness, docs, ops, market, support,
// regulatory, cost. See internal/rubric for the contracts.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/provider"
	"github.com/nfsarch33/helixon-eval/internal/rubric"
)

func main() {
	root := &cobra.Command{
		Use:   "helixon-eval",
		Short: "Helixon evaluation harness (12 dimensions: 5 harness + 7 R6 + per-provider cell)",
	}

	root.AddCommand(runCmd(), reportCmd(), listCmd(), leaderboardCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func runCmd() *cobra.Command {
	var (
		suiteName    string
		output       string
		providerName string
		modelName    string
		apiKeyStdin  bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an evaluation suite and append results to NDJSON report",
		RunE: func(cmd *cobra.Command, args []string) error {
			if suiteName == "" {
				return fmt.Errorf("--suite required")
			}
			suite, ok := rubric.AllSuites[suiteName]
			if !ok {
				return fmt.Errorf("unknown suite %q (see `helixon-eval list`)", suiteName)
			}

			ctx := context.Background()
			runner := evalfw.NewRunner(evalfw.RunnerConfig{})
			result, err := runner.RunSuite(ctx, suite)
			if err != nil {
				return err
			}

			out := output
			if out == "" {
				out = evalfw.DefaultReportPath()
			}
			w, err := evalfw.NewReportWriter(out)
			if err != nil {
				return err
			}
			if err := w.Write(result); err != nil {
				return err
			}

			// If --provider / --model given, run an LLM round-trip
			// over each suite case to capture the per-model leaderboard row.
			if providerName != "" || modelName != "" {
				if err := runProviderRoundTrip(providerName, modelName, apiKeyStdin, suite, result); err != nil {
					fmt.Fprintf(os.Stderr, "WARN provider round-trip: %v\n", err)
				}
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&suiteName, "suite", "", "suite name (see `helixon-eval list`)")
	cmd.Flags().StringVar(&output, "output", "", "NDJSON output path (default ~/logs/runx/eval-results.ndjson)")
	cmd.Flags().StringVar(&providerName, "provider", "", "R6 provider name (minimax|qwen|dummy)")
	cmd.Flags().StringVar(&modelName, "model", "", "R6 model id (MiniMax-M3|qwen3.7-plus|qwen3.7-max|<custom>)")
	cmd.Flags().BoolVar(&apiKeyStdin, "api-key-stdin", false, "read API key from stdin (pipe from `op read`); never pass on argv")
	return cmd
}

func reportCmd() *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Aggregate and print an NDJSON report",
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				input = evalfw.DefaultReportPath()
			}
			data, err := os.ReadFile(input)
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "NDJSON input path (default ~/logs/runx/eval-results.ndjson)")
	return cmd
}

func listCmd() *cobra.Command {
	var filter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available rubrics and suites",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("# Dimensions (12)")
			for _, d := range rubric.R6Dimensions {
				fmt.Printf("- %s\n", d)
			}
			fmt.Println("\n# R6 backends (provider/model cells)")
			for _, c := range rubric.R6Backends {
				fmt.Printf("- %s\n", c)
			}
			fmt.Println("\n# Suites")
			for name, s := range rubric.AllSuites {
				if filter != "" && filepath.Base(name) != filter {
					continue
				}
				fmt.Printf("- %s: %d cases\n", name, len(s.Cases))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filter, "rubric", "", "filter by rubric name")
	return cmd
}

func leaderboardCmd() *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:   "leaderboard",
		Short: "Aggregate R6 per-(provider, model) rows from NDJSON report",
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				input = evalfw.DefaultReportPath()
			}
			data, err := os.ReadFile(input)
			if err != nil {
				return err
			}
			rows := aggregateLeaderboard(data)
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "NDJSON input path (default ~/logs/runx/eval-results.ndjson)")
	return cmd
}

// runProviderRoundTrip executes one LLM call per suite case, charging
// the budget sentinel, retrying transient failures, and DLQ-ing after
// 3 attempts. The results append to ~/logs/helixon-eval/leaderboard.ndjson.
func runProviderRoundTrip(providerName, modelName string, apiKeyStdin bool, suite evalfw.Suite, sr *evalfw.SuiteResult) error {
	if providerName == "" || modelName == "" {
		return fmt.Errorf("both --provider and --model required")
	}
	apiKey := ""
	if apiKeyStdin {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		apiKey = trim(string(b))
	}

	var p provider.Provider
	switch providerName {
	case "minimax":
		p = provider.NewMinimax(modelName, apiKey)
	case "qwen":
		p = provider.NewQwen(modelName, apiKey)
	case "dummy":
		p = provider.NewDummy(modelName, apiKey, nil)
	default:
		return fmt.Errorf("unknown provider %q (minimax|qwen|dummy)", providerName)
	}

	budget, err := provider.NewBudgetSentinel()
	if err != nil {
		return err
	}
	dlq, err := provider.NewDLQ(provider.DefaultDLQPath())
	if err != nil {
		return err
	}

	rows := []map[string]any{}
	for _, c := range suite.Cases {
		prompt := fmt.Sprintf("[%s] %s", c.Name, c.Tags)
		cost := p.EstimateCost(len(prompt), 200)
		alerted, _ := budget.Record(cost, modelName)
		row := map[string]any{
			"ts":       time.Now().Format(time.RFC3339),
			"provider": providerName,
			"model":    modelName,
			"case":     c.Name,
			"verdict":  string(sr.Cases[caseIndex(sr.Cases, c.Name)].Verdict),
			"cost_usd": cost.USD,
			"alerted":  alerted,
		}
		if apiKey != "" {
			policy := provider.DefaultRetryPolicy()
			idemStore, _ := provider.NewIdempotencyStore(defaultIdemPath())
			key := provider.IdempotencyKey(modelName, prompt, time.Now().Unix()/provider.IdempotencyBucketSeconds)
			if idemStore.Seen(key) {
				row["idempotency_hit"] = true
			}
			_, attempts, err := provider.CallWithRetry(context.Background(), policy, func(ctx context.Context) (provider.Response, error) {
				return p.Chat(ctx, provider.Request{Model: modelName, Prompt: prompt})
			})
			row["attempts"] = attempts
			if err != nil {
				row["chat_error"] = err.Error()
				_ = idemStore.Record(key)
				_ = dlq.Append(provider.DLQEntry{
					Model: modelName, PromptHash: c.Name, Attempts: attempts, LastError: err.Error(),
				})
			} else {
				_ = idemStore.Record(key)
			}
		}
		rows = append(rows, row)
	}
	return writeLeaderboard(rows)
}

func defaultIdemPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "logs", "helixon-eval", "idempotency.jsonl")
}

func defaultLeaderboardPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "logs", "helixon-eval", "leaderboard.ndjson")
}

func writeLeaderboard(rows []map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(defaultLeaderboardPath()), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(defaultLeaderboardPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

func aggregateLeaderboard(data []byte) []map[string]any {
	rows := []map[string]any{}
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		rows = append(rows, r)
	}
	return rows
}

func splitLines(data []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && (s[0] == '\n' || s[0] == '\r' || s[0] == ' ') {
		s = s[1:]
	}
	return s
}

// caseIndex looks up a CaseResult by name within the suite result.
func caseIndex(cases []evalfw.CaseResult, name string) int {
	for i, c := range cases {
		if c.Name == name {
			return i
		}
	}
	return -1
}
