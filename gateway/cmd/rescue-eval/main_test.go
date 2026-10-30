package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rescue-gateway/gateway/pkg/metrics"
)

func TestEvaluationCasesAreMatchedAndVaryZombieFraction(t *testing.T) {
	cases := evaluationCases()
	if len(cases) != 6 {
		t.Fatalf("evaluation cases = %d, want 6", len(cases))
	}
	for i := 0; i < len(cases); i += 2 {
		rescue, baseline := cases[i], cases[i+1]
		if rescue.Name != baseline.Name ||
			rescue.Rate != baseline.Rate ||
			rescue.Workers != baseline.Workers ||
			rescue.WorkDuration != baseline.WorkDuration ||
			rescue.Deadline != baseline.Deadline ||
			rescue.ZombieFraction != baseline.ZombieFraction {
			t.Fatalf("policy pair is not workload-matched: %+v vs %+v", rescue, baseline)
		}
		if rescue.Policy != "rescue" || baseline.Policy != "baseline" ||
			rescue.BaselineMode || !baseline.BaselineMode {
			t.Fatalf("unexpected policies in matched pair: %+v / %+v", rescue, baseline)
		}
		if rescue.Rate != 40 || rescue.Workers != 4 ||
			rescue.Deadline.String() != "120ms" ||
			!rescue.FirstContact || !rescue.ZombieAllTraffic {
			t.Fatalf("unexpected common workload configuration: %+v", rescue)
		}
	}
	if cases[0].ZombieFraction != 0 ||
		cases[2].ZombieFraction != 0.10 ||
		cases[4].ZombieFraction != 0.30 {
		t.Fatalf("zombie fractions = %.2f, %.2f, %.2f; want 0, 0.10, 0.30",
			cases[0].ZombieFraction, cases[2].ZombieFraction, cases[4].ZombieFraction)
	}
}

func TestReportRetainsFailedRunsAndSummarizesSuccessfulRuns(t *testing.T) {
	results := []evalResult{
		{
			CaseName: "zombies-10pct",
			Policy:   "rescue",
			Seed:     10,
			Status:   "completed",
			Summary: metrics.Summary{
				LegitimateDeadlineSuccessRate: 0.8,
				MaxUnsafeError:                2,
				AvgAccountingError:            0.5,
				Goodput:                       30,
				P95Latency:                    90,
			},
		},
		{
			CaseName:  "zombies-10pct",
			Policy:    "rescue",
			Seed:      11,
			Status:    "failed",
			Error:     "benchmark process failed",
			OutputDir: filepath.Join("results", "failed-run"),
		},
	}
	path := filepath.Join(t.TempDir(), "report.md")
	if err := generateMarkdownReport(path, results, 2, 5_000_000_000); err != nil {
		t.Fatalf("generate report: %v", err)
	}
	report, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	content := string(report)
	for _, expected := range []string{
		"| 1 / 1 |",
		"benchmark process failed",
		"seed 11",
		"Mean S_L",
		"all traffic",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
}
