// Command sim runs load test scenarios on the real server and prints what they cost: region
// tick times, the parts of the tick, allocations and garbage collector CPU. Each count runs
// on a fresh server in a temporary folder, with no network.
//
//	go run ./cmd/sim -list
//	go run ./cmd/sim -scenario tnt -n 0,100,1000
//	go run ./cmd/sim -scenario all -n 0,1000 -cores 4 -json results.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/helix-bedrock/helix/sim"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sim:", err)
		os.Exit(1)
	}
}

// options are the command line flags.
type options struct {
	scenario, counts, jsonPath string
	list                       bool
	size                       int
	conf                       sim.Config
}

// parseFlags reads the command line.
func parseFlags() options {
	var o options
	flag.StringVar(&o.scenario, "scenario", "idle", "scenario name, or all")
	flag.StringVar(&o.counts, "n", "0,100,1000", "comma-separated unit counts; each runs on a fresh server")
	flag.StringVar(&o.jsonPath, "json", "", "also write the reports as JSON to this file")
	flag.BoolVar(&o.list, "list", false, "list the scenarios and exit")
	flag.IntVar(&o.size, "size", 0, "side in blocks of the loaded square (0: the scenario's own)")
	flag.IntVar(&o.conf.Cores, "cores", 0, "tick cores (0: one per CPU)")
	flag.StringVar(&o.conf.WorldType, "world", "", "world type (empty: flat)")
	flag.DurationVar(&o.conf.Warmup, "warmup", sim.DefaultWarmup, "how long the load runs before measuring")
	flag.DurationVar(&o.conf.Measure, "measure", sim.DefaultMeasure, "how long the load is measured")
	flag.Parse()
	return o
}

func run() error {
	o := parseFlags()
	if o.list {
		return listScenarios(os.Stdout)
	}
	scenarios, err := pick(o.scenario, o.size)
	if err != nil {
		return err
	}
	counts, err := parseCounts(o.counts)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var all []sim.Report
	for _, sc := range scenarios {
		fmt.Fprintf(os.Stderr, "running %s for n=%s...\n", sc.Name, o.counts)
		reps, err := sim.Sweep(ctx, o.conf, sc, counts)
		all = append(all, reps...)
		if err != nil {
			return errors.Join(err, writeResults(all, o.jsonPath))
		}
	}
	return writeResults(all, o.jsonPath)
}

// listScenarios prints every scenario's name and what it is.
func listScenarios(out io.Writer) error {
	for _, sc := range sim.Scenarios() {
		if _, err := fmt.Fprintf(out, "%-10s %s\n", sc.Name, sc.About); err != nil {
			return err
		}
	}
	return nil
}

// pick is the scenarios named, all of them for "all", with the area size set when size
// is not 0.
func pick(name string, size int) ([]sim.Scenario, error) {
	var out []sim.Scenario
	if name == "all" {
		out = sim.Scenarios()
	} else {
		sc, ok := sim.Find(name)
		if !ok {
			return nil, fmt.Errorf("no scenario %q (try -list)", name)
		}
		out = []sim.Scenario{sc}
	}
	if size > 0 {
		for i := range out {
			out[i].Size = int32(size) //nolint:gosec // a flag
		}
	}
	return out, nil
}

// parseCounts reads "0,100,1000".
func parseCounts(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad count %q in -n", p)
		}
		out = append(out, n)
	}
	return out, nil
}

// writeResults prints the table and, if asked, writes the JSON file.
func writeResults(reps []sim.Report, jsonPath string) error {
	if len(reps) == 0 {
		return nil
	}
	if err := sim.WriteTable(os.Stdout, reps); err != nil {
		return err
	}
	if jsonPath == "" {
		return nil
	}
	data, err := json.MarshalIndent(reps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(jsonPath, data, 0o600)
}
