// cmd/demo — pilot E2E smoke harness entrypoint (v18688-2, v18692-5).
//
// Usage:
//
//	cd cmd/demo && go run .                          # MiniMax-M3 dry-run
//	cd cmd/demo && go run . --provider qwen3.7-plus  # single-provider matrix
//	cd cmd/demo && go run . --all-providers          # all 3-provider matrix
//	cd cmd/demo && go run . --json                   # NDJSON envelope per task
//	HELIXON_LIVE_EVAL=1 go run . --all-providers     # real round-trip (gated)
//
// v18692-5 adds:
//
//	--provider {minimaxi|qwen3.7-plus|qwen3.7-max}
//	--all-providers
//	--live (or set HELIXON_LIVE_EVAL=1)
//	--ndjson-out <path>  (default ~/logs/runx/helixon-eval-matrix.ndjson)
//
// The harness uses demo.DefaultPilotTasks() as the canonical 7-task
// demo. For live API round-trip, gate on HELIXON_LIVE_EVAL=1 and
// route through the provider's canonical endpoint
// (api.minimaxi.com or cn-beijing.maas.aliyuncs.com/compatible-mode/v1).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/demo"
)

func main() {
	jsonFlag := flag.Bool("json", false, "emit NDJSON envelope per task")
	budget := flag.Float64("budget-usd", 0.05, "cost budget in USD (gate)")
	wallClock := flag.Duration("wallclock", 10*time.Second, "wall-clock budget")
	p99 := flag.Duration("p99-latency", 3*time.Second, "p99 latency budget")
	provider := flag.String("provider", "minimaxi",
		"single-provider target (minimaxi|qwen3.7-plus|qwen3.7-max)")
	allFlag := flag.Bool("all-providers", false,
		"run the matrix across all 3 providers")
	liveFlag := flag.Bool("live", false,
		"execute real round-trip (gated; same as HELIXON_LIVE_EVAL=1)")
	apiKeyFlag := flag.String("api-key", "",
		"override API key for the active provider (otherwise reads env)")
	ndjsonOut := flag.String("ndjson-out", defaultNDJSONPath(),
		"NDJSON matrix output path")
	matrixOnly := flag.Bool("matrix", false,
		"only run the multi-provider matrix (skip the 1-provider pilot demo)")
	flag.Parse()

	useLive := *liveFlag || strings.TrimSpace(os.Getenv("HELIXON_LIVE_EVAL")) == "1"

	// ── v18688-2 path: 1-provider pilot demo, no extra arguments.
	if !*allFlag && !*matrixOnly &&
		*provider != "" && !useLive &&
		!*jsonFlag && *apiKeyFlag == "" &&
		*ndjsonOut == defaultNDJSONPath() {
		runLegacyPilot(jsonFlag, budget, wallClock, p99)
		return
	}

	// ── v18692-5 path: multi-provider matrix.
	opts := &matrixOpts{
		providerFlag: *provider,
		allFlag:      *allFlag,
		useLive:      useLive,
		apiKeyFlag:   *apiKeyFlag,
		ndjsonOut:    *ndjsonOut,
		matrixOnly:   *matrixOnly,
		jsonFlag:     *jsonFlag,
		budget:       *budget,
		wallClock:    *wallClock,
		p99:          *p99,
	}
	if err := runMatrix(opts); err != nil {
		fmt.Fprintf(os.Stderr, "matrix failed: %v\n", err)
		os.Exit(1)
	}
}

// runLegacyPilot preserves the v18688-2 single-provider pilot demo.
func runLegacyPilot(jsonFlag *bool, budget *float64, wallClock, p99 *time.Duration) {
	bundle, err := demo.PilotDemo(demo.DefaultPilotTasks(), demo.DefaultMiniMaxPricing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}

	if *jsonFlag {
		os.Stdout.Write(demo.NDJSONEntries(bundle))
	}

	pass, reason := demo.Gates(bundle, *budget, *wallClock, *p99)
	fmt.Printf("=== v18688-2 Pilot E2E ===\n")
	fmt.Printf("tasks: %d\n", len(bundle.Tasks))
	fmt.Printf("total_cost_usd: $%.6f (budget $%.4f)\n", bundle.TotalCostUSD, *budget)
	fmt.Printf("wall_clock: %s (budget %s)\n", bundle.TotalLatency, *wallClock)
	fmt.Printf("p99_latency: %s (budget %s)\n", bundle.P99Latency, *p99)
	fmt.Printf("gates: %s — %s\n", map[bool]string{true: "GREEN", false: "RED"}[pass], reason)

	if !pass {
		os.Exit(1)
	}
}

// runMatrix dispatches the v18692-5 multi-provider matrix path.
type matrixOpts struct {
	providerFlag string
	allFlag      bool
	useLive      bool
	apiKeyFlag   string
	ndjsonOut    string
	matrixOnly   bool
	jsonFlag     bool
	budget       float64
	wallClock    time.Duration
	p99          time.Duration
}

func runMatrix(opts *matrixOpts) error {
	var providers []demo.Provider
	if opts.allFlag {
		providers = demo.AllProviders()
	} else {
		p, err := demo.ParseProvider(opts.providerFlag)
		if err != nil {
			return err
		}
		providers = []demo.Provider{p}
	}

	keys := demo.ResolveAPIKeysFor(providers, overrideMap(providers, opts.apiKeyFlag), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := demo.RunMatrix(ctx, providers, keys, opts.useLive)
	if err != nil {
		return err
	}

	// Write matrix NDJSON envelope.
	if err := ensureDir(opts.ndjsonOut); err != nil {
		return err
	}
	if err := demo.AppendNDJSON(opts.ndjsonOut, result); err != nil {
		return fmt.Errorf("append ndjson: %w", err)
	}

	// Optional: --json per-task NDJSON for the first provider with ok status.
	if opts.jsonFlag {
		for _, r := range result.Rows {
			if r.Status == "ok" {
				os.Stdout.Write(demo.NDJSONEntries(&demo.PilotBundle{
					Tasks: r.Tasks,
				}))
				break
			}
		}
	}

	// If the matrixOnly flag is set, dump markdown + exit; otherwise
	// also run the legacy pilot for the first provider (back-compat).
	fmt.Println(demo.RenderMarkdown(result))
	if !opts.matrixOnly {
		if len(providers) > 0 {
			p := providers[0]
			tasks := demo.DefaultPilotTasks()
			for i := range tasks {
				tasks[i].Model = p.Model()
			}
			pp := demo.PricingFor(p).ToPilotPricing()
			bundle, perr := demo.PilotDemo(tasks, pp)
			if perr != nil {
				return perr
			}
			pass, reason := demo.Gates(bundle, opts.budget, opts.wallClock, opts.p99)
			fmt.Printf("\n=== Back-compat Pilot (%s) ===\n", p)
			fmt.Printf("gates: %s — %s\n", map[bool]string{true: "GREEN", false: "RED"}[pass], reason)
			if !pass {
				return errors.New("back-compat pilot FAIL")
			}
		}
	}
	return nil
}

// overrideMap converts the --api-key flag into the per-provider map
// expected by ResolveAPIKeysFor. Empty flag yields an empty map.
func overrideMap(providers []demo.Provider, apiKeyFlag string) map[demo.Provider]string {
	apiKey := strings.TrimSpace(apiKeyFlag)
	if apiKey == "" {
		return nil
	}
	out := make(map[demo.Provider]string)
	for _, p := range providers {
		out[p] = apiKey
	}
	return out
}

// ensureDir makes sure the parent directory of path exists so the
// NDJSON write cannot fail on ENOENT.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0o755)
}

// defaultNDJSONPath returns ~/logs/runx/helixon-eval-matrix.ndjson.
// Falls back to /tmp if HOME is unset (defensive).
func defaultNDJSONPath() string {
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, "logs", "runx", "helixon-eval-matrix.ndjson")
}

// errMatrix is a sentinel for the matrix run path; kept here so callers
// can match on a stable type even when no surface is exposed.
var _ error = errors.New("matrix")

// WriteAll is the io.Writer helper used by RenderMarkdown output.
var _ io.Writer = io.Discard
