// Command telemetry-policy-sim runs policy scenario files and reports each
// expectation, so a day can be checked before any device enforces it.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rimexe0/telemetry/internal/policy"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: telemetry-policy-sim [-json] scenario.json...")
		os.Exit(2)
	}
	asJSON := false
	files := os.Args[1:]
	if files[0] == "-json" {
		asJSON, files = true, files[1:]
	}
	failed := false
	results := []policy.ScenarioResult{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		scenario, err := policy.ParseScenario(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", file, err)
			os.Exit(2)
		}
		result := policy.RunScenario(scenario)
		results = append(results, result)
		failed = failed || !result.Pass
		if asJSON {
			continue
		}
		status := "PASS"
		if !result.Pass {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, result.Name)
		if result.Error != "" {
			fmt.Printf("  error: %s\n", result.Error)
		}
		for _, item := range result.Results {
			mark := "ok  "
			if !item.Pass {
				mark = "FAIL"
			}
			fmt.Printf("  %s %s %-9s %s\n", mark, item.At, item.Decision.Mode, item.Decision.Reason)
			for _, failure := range item.Failures {
				fmt.Printf("       - %s\n", failure)
			}
		}
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(results)
	}
	if failed {
		os.Exit(1)
	}
}
