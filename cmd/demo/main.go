// cmd/demo — pilot E2E smoke harness entrypoint (v18688-2).
//
// Usage:
//
//	cd cmd/demo && go run .            # prints bundle summary to stdout
//	cd cmd/demo && go run . -json      # NDJSON envelope per task
//
// The harness uses demo.DefaultPilotTasks() as the canonical 7-task
// demo. For live API round-trip, gate on HELIXON_DEMO_LIVE_TEST=1
// and route through llm-cluster-router's `minimax-chat` listener
// (configured by configs/router.minimax.live.yml).
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nfsarch33/helixon-eval/internal/demo"
)

func main() {
	jsonFlag := flag.Bool("json", false, "emit NDJSON envelope per task")
	budget := flag.Float64("budget-usd", 0.05, "cost budget in USD (gate)")
	wallClock := flag.Duration("wallclock", 10*time.Second, "wall-clock budget")
	p99 := flag.Duration("p99-latency", 3*time.Second, "p99 latency budget")
	flag.Parse()

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
