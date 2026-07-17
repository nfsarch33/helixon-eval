// Command helixon-eval is the operator entry point for the Helixon
// evaluation harness. It exposes three subcommands:
//
//	helixon-eval run     -- run an evaluation suite and append to NDJSON report
//	helixon-eval report  -- aggregate and print NDJSON report
//	helixon-eval list    -- list available rubrics and suites
//
// Every Case registered against a Suite implements one of the 5 rubrics
// (reliability, observability, security, test coverage, task completion).
// See internal/rubric for the rubric contracts.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nfsarch33/helixon-eval/internal/evalfw"
	"github.com/nfsarch33/helixon-eval/internal/rubric"
)

func main() {
	root := &cobra.Command{
		Use:   "helixon-eval",
		Short: "Helixon evaluation harness (5 rubrics: reliability, observability, security, test coverage, task completion)",
	}

	root.AddCommand(runCmd(), reportCmd(), listCmd(), demoCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func runCmd() *cobra.Command {
	var (
		suiteName string
		output    string
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
			fmt.Println("# Rubrics")
			for _, r := range rubric.All {
				fmt.Printf("- %s: %s\n", r.Name, r.Description)
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
