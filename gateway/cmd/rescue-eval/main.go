package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rescue-gateway/gateway/pkg/metrics"
)

type evalCase struct {
	Name             string
	Policy           string
	BaselineMode     bool
	Rate             float64
	Workers          int
	WorkDuration     time.Duration
	Deadline         time.Duration
	ZombieFraction   float64
	ZombieHangFactor float64
	FirstContact     bool
	ZombieAllTraffic bool
}

type evalResult struct {
	CaseName       string          `json:"case_name"`
	Policy         string          `json:"policy"`
	Seed           int64           `json:"seed"`
	Status         string          `json:"status"`
	Error          string          `json:"error,omitempty"`
	Controller     string          `json:"controller,omitempty"`
	OfferedRate    float64         `json:"offered_rate"`
	ZombieFraction float64         `json:"zombie_fraction"`
	Summary        metrics.Summary `json:"summary,omitempty"`
	OutputDir      string          `json:"output_dir"`
}

type aggregateResult struct {
	CaseName        string
	Policy          string
	Rate            float64
	Zombies         float64
	Succeeded       int
	Failed          int
	SLTotal         float64
	UnsafeTotal     float64
	AccountingTotal float64
	GoodputTotal    float64
	P95Total        float64
}

func main() {
	benchDuration := flag.Duration("duration", 5*time.Second, "duration per benchmark run")
	seed := flag.Int64("seed", 20261028, "first seed in the sweep; matched policies use the same seed")
	seeds := flag.Int("seeds", 10, "number of matched seeds per workload and policy")
	outDir := flag.String("out", filepath.Join("..", "results", "comparative-eval-"+time.Now().UTC().Format("20060102T150405Z")), "directory for all eval results")
	flag.Parse()

	if *benchDuration <= 0 {
		fmt.Fprintln(os.Stderr, "duration must be positive")
		os.Exit(1)
	}
	if *seeds <= 0 {
		fmt.Fprintln(os.Stderr, "seeds must be positive")
		os.Exit(1)
	}
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	cases := evaluationCases()
	totalRuns := len(cases) * *seeds
	fmt.Printf("Starting matched comparative evaluation: %d workloads, %d seeds, %d runs\n",
		len(cases)/2, *seeds, totalRuns)
	fmt.Printf("Rate=40 rps, workers/pool=4, work=40ms with benchmark jitter, deadline=120ms\n")
	fmt.Printf("Output directory: %s\n\n", *outDir)

	results := make([]evalResult, 0, totalRuns)
	for seedIndex := 0; seedIndex < *seeds; seedIndex++ {
		runSeed := *seed + int64(seedIndex)
		for caseIndex, c := range cases {
			outputDir := filepath.Join(*outDir, c.Name, c.Policy, fmt.Sprintf("seed-%d", runSeed))
			result := runEvalCase(c, runSeed, outputDir, *benchDuration)
			results = append(results, result)

			runNumber := seedIndex*len(cases) + caseIndex + 1
			if result.Status == "completed" {
				fmt.Printf("[%d/%d] %s / %s / seed %d: S_L=%.4f, sampled max unsafe error=%d\n",
					runNumber, totalRuns, c.Name, c.Policy, runSeed,
					result.Summary.LegitimateDeadlineSuccessRate, result.Summary.MaxUnsafeError)
			} else {
				fmt.Fprintf(os.Stderr, "[%d/%d] %s / %s / seed %d failed: %s\n",
					runNumber, totalRuns, c.Name, c.Policy, runSeed, result.Error)
			}
		}
	}

	reportPath := filepath.Join(*outDir, "comparative_report.json")
	reportBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal comparative results: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(reportPath, reportBytes, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write comparative results: %v\n", err)
		os.Exit(1)
	}

	mdPath := filepath.Join(*outDir, "COMPARATIVE_REPORT.md")
	if err := generateMarkdownReport(mdPath, results, *seeds, *benchDuration); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write comparative report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nEvaluation complete. Reports:\n  %s\n  %s\n", reportPath, mdPath)
	for _, result := range results {
		if result.Status != "completed" {
			fmt.Fprintf(os.Stderr, "evaluation contains failed runs; see %s\n", mdPath)
			os.Exit(1)
		}
	}
}

func evaluationCases() []evalCase {
	var cases []evalCase
	for _, workload := range []struct {
		name           string
		zombieFraction float64
	}{
		{name: "zombies-0pct", zombieFraction: 0},
		{name: "zombies-10pct", zombieFraction: 0.10},
		{name: "zombies-30pct", zombieFraction: 0.30},
	} {
		for _, policy := range []struct {
			name         string
			baselineMode bool
		}{
			{name: "rescue"},
			{name: "baseline", baselineMode: true},
		} {
			cases = append(cases, evalCase{
				Name:             workload.name,
				Policy:           policy.name,
				BaselineMode:     policy.baselineMode,
				Rate:             40,
				Workers:          4,
				WorkDuration:     40 * time.Millisecond,
				Deadline:         120 * time.Millisecond,
				ZombieFraction:   workload.zombieFraction,
				ZombieHangFactor: 2.5,
				FirstContact:     true,
				ZombieAllTraffic: true,
			})
		}
	}
	return cases
}

func runEvalCase(c evalCase, seed int64, outputDir string, duration time.Duration) evalResult {
	result := evalResult{
		CaseName:       c.Name,
		Policy:         c.Policy,
		Seed:           seed,
		Status:         "failed",
		OfferedRate:    c.Rate,
		ZombieFraction: c.ZombieFraction,
		OutputDir:      outputDir,
	}
	args := []string{
		"run", "./cmd/rescue-bench",
		"-duration", duration.String(),
		"-rate", fmt.Sprintf("%.1f", c.Rate),
		"-workers", fmt.Sprintf("%d", c.Workers),
		"-work", c.WorkDuration.String(),
		"-deadline", c.Deadline.String(),
		"-seed", fmt.Sprintf("%d", seed),
		"-zombie-fraction", fmt.Sprintf("%.2f", c.ZombieFraction),
		"-zombie-hang-factor", fmt.Sprintf("%.2f", c.ZombieHangFactor),
		"-out", outputDir,
	}
	if c.FirstContact {
		args = append(args, "-first-contact")
	}
	if c.ZombieAllTraffic {
		args = append(args, "-zombie-all-traffic")
	}
	if c.BaselineMode {
		args = append(args, "-baseline-timed-release")
	}

	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		result.Error = fmt.Sprintf("execute benchmark: %v", err)
		return result
	}

	summaryData, err := os.ReadFile(filepath.Join(outputDir, "summary.json"))
	if err != nil {
		result.Error = fmt.Sprintf("read summary: %v", err)
		return result
	}
	if err := json.Unmarshal(summaryData, &result.Summary); err != nil {
		result.Error = fmt.Sprintf("parse summary: %v", err)
		return result
	}
	result.Controller = result.Summary.Controller
	result.Status = "completed"
	return result
}

func generateMarkdownReport(path string, results []evalResult, seeds int, duration time.Duration) error {
	var report strings.Builder
	fmt.Fprintf(&report, "# RESCUE vs Timed-Release Baseline Evaluation\n\n")
	fmt.Fprintf(&report, "**Date:** %s  \n**Benchmark duration:** %s per run  \n**Matched seeds per workload/policy:** %d\n\n",
		time.Now().Format("2006-01-02 15:04:05"), duration, seeds)
	fmt.Fprintf(&report, "All workloads use 40 offered requests/s, four workers per pool, 40 ms base work with benchmark jitter, and a 120 ms deadline. Zombie fraction is applied to all traffic. Both policies use first-contact routing, so this comparison does not measure normal-pool isolation.\n\n")

	aggregates := aggregateResults(results)
	fmt.Fprintf(&report, "## Results by Workload and Policy\n\n")
	fmt.Fprintf(&report, "| Workload | Policy | Completed / Failed | Mean S_L | Mean max sampled unsafe error | Mean accounting error | Mean goodput (rps) | Mean p95 latency (ms) |\n")
	fmt.Fprintf(&report, "|:---|:---:|:---:|---:|---:|---:|---:|---:|\n")
	for _, aggregate := range aggregates {
		meanSL := meanOrNA(aggregate.SLTotal, aggregate.Succeeded, "%.4f")
		meanUnsafe := meanOrNA(aggregate.UnsafeTotal, aggregate.Succeeded, "%.2f")
		meanAccounting := meanOrNA(aggregate.AccountingTotal, aggregate.Succeeded, "%.2f")
		meanGoodput := meanOrNA(aggregate.GoodputTotal, aggregate.Succeeded, "%.2f")
		meanP95 := meanOrNA(aggregate.P95Total, aggregate.Succeeded, "%.2f")
		fmt.Fprintf(&report, "| %s (%.0f%% zombies) | %s | %d / %d | %s | %s | %s | %s | %s |\n",
			aggregate.CaseName, aggregate.Zombies*100, aggregate.Policy,
			aggregate.Succeeded, aggregate.Failed, meanSL, meanUnsafe, meanAccounting, meanGoodput, meanP95)
	}

	fmt.Fprintf(&report, "\n`Max unsafe error` is a periodic-snapshot accounting metric, not direct evidence of slot reuse. Means use successful runs only; failures are counted separately and listed below.\n\n")
	fmt.Fprintf(&report, "## Failed Runs\n\n")
	failureCount := 0
	for _, result := range results {
		if result.Status == "completed" {
			continue
		}
		failureCount++
		fmt.Fprintf(&report, "- **%s / %s / seed %d** (`%s`): %s\n",
			result.CaseName, result.Policy, result.Seed, result.OutputDir,
			strings.ReplaceAll(result.Error, "|", "\\|"))
	}
	if failureCount == 0 {
		fmt.Fprintf(&report, "None.\n")
	}
	return os.WriteFile(path, []byte(report.String()), 0644)
}

func aggregateResults(results []evalResult) []aggregateResult {
	var aggregates []aggregateResult
	indices := make(map[string]int)
	for _, result := range results {
		key := result.CaseName + "\x00" + result.Policy
		index, exists := indices[key]
		if !exists {
			index = len(aggregates)
			indices[key] = index
			aggregates = append(aggregates, aggregateResult{
				CaseName: result.CaseName,
				Policy:   result.Policy,
				Rate:     result.OfferedRate,
				Zombies:  result.ZombieFraction,
			})
		}
		aggregate := &aggregates[index]
		if result.Status != "completed" {
			aggregate.Failed++
			continue
		}
		aggregate.Succeeded++
		aggregate.SLTotal += result.Summary.LegitimateDeadlineSuccessRate
		aggregate.UnsafeTotal += float64(result.Summary.MaxUnsafeError)
		aggregate.AccountingTotal += result.Summary.AvgAccountingError
		aggregate.GoodputTotal += result.Summary.Goodput
		aggregate.P95Total += result.Summary.P95Latency
	}
	return aggregates
}

func meanOrNA(total float64, count int, format string) string {
	if count == 0 {
		return "n/a"
	}
	return fmt.Sprintf(format, total/float64(count))
}
