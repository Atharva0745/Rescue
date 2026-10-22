package metrics

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func validRecord(id string, base time.Time) RequestRecord {
	arrived := base
	admitted := arrived.Add(time.Millisecond)
	started := admitted.Add(time.Millisecond)
	completed := started.Add(20 * time.Millisecond)
	return RequestRecord{
		RequestID:    id,
		SourceID:     "source-1",
		IsLegitimate: true,
		ArrivedAt:    arrived,
		Deadline:     arrived.Add(time.Second),
		AdmittedAt:   admitted,
		StartedAt:    started,
		CompletedAt:  completed,
		TotalLatency: completed.Sub(arrived),
		Outcome:      "completed",
		WasAdmitted:  true,
		MetDeadline:  true,
	}
}

func TestSuccessRequiresCompletedAdmittedBeforeDeadline(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := NewCollector("rescue", "run-1", 1)
	success := validRecord("success", base)
	if err := c.Record(success); err != nil {
		t.Fatalf("record success: %v", err)
	}

	timedOut := validRecord("timeout", base.Add(time.Second))
	timedOut.Outcome = "timed_out"
	timedOut.MetDeadline = false
	timedOut.CompletedAt = timedOut.Deadline.Add(time.Second)
	timedOut.TotalLatency = timedOut.CompletedAt.Sub(timedOut.ArrivedAt)
	if err := c.Record(timedOut); err != nil {
		t.Fatalf("record timeout: %v", err)
	}
	if got := c.LegitimateDeadlineSuccessRate(); got != 0.5 {
		t.Fatalf("success rate = %v, want 0.5", got)
	}
	p50, _, _ := c.LatencyPercentiles()
	if want := success.CompletedAt.Sub(success.ArrivedAt); p50 != want {
		t.Fatalf("successful latency p50 = %v, want %v", p50, want)
	}

	inconsistent := validRecord("inconsistent", base.Add(2*time.Second))
	inconsistent.Outcome = "timed_out"
	if err := c.Record(inconsistent); err == nil {
		t.Fatal("accepted a timeout record claiming it met its deadline")
	}
}

func TestGoodputUsesFixedMeasurementWindowForSingleSuccess(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := NewCollector("rescue", "run-1", 1)
	window := MeasurementWindow{Start: base, End: base.Add(10 * time.Second)}
	if err := c.SetMeasurementWindow(window); err != nil {
		t.Fatalf("set measurement window: %v", err)
	}

	inside := validRecord("inside", base)
	inside.CompletedAt = base.Add(200 * time.Millisecond)
	inside.TotalLatency = inside.CompletedAt.Sub(inside.ArrivedAt)
	if err := c.Record(inside); err != nil {
		t.Fatalf("record in-window success: %v", err)
	}
	outside := validRecord("outside", base.Add(10*time.Second))
	outside.CompletedAt = base.Add(10*time.Second + 200*time.Millisecond)
	outside.TotalLatency = outside.CompletedAt.Sub(outside.ArrivedAt)
	if err := c.Record(outside); err != nil {
		t.Fatalf("record out-of-window success: %v", err)
	}

	if got := c.Goodput(); got != 0.1 {
		t.Fatalf("goodput = %v, want 0.1 completions/second", got)
	}
	if p50, _, _ := c.LatencyPercentiles(); p50 != inside.CompletedAt.Sub(inside.ArrivedAt) {
		t.Fatalf("latency percentile included an out-of-window request: %v", p50)
	}
	summary := c.ComputeSummary()
	if summary.Goodput != 0.1 || summary.LegitimateGoodput != 0.1 || summary.MeasurementWindow != window {
		t.Fatalf("summary did not use configured window: %+v", summary)
	}
	if err := c.SetMeasurementWindow(MeasurementWindow{Start: base, End: base.Add(time.Minute)}); err == nil {
		t.Fatal("measurement window changed after configuration")
	}
}

func TestSummaryComputesLegitimateFirstContactSuccessSeparately(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := NewCollector("rescue", "run-first-contact", 1)
	window := MeasurementWindow{Start: base, End: base.Add(time.Minute)}
	if err := c.SetMeasurementWindow(window); err != nil {
		t.Fatalf("set measurement window: %v", err)
	}

	firstContactSuccess := validRecord("first-success", base.Add(time.Second))
	firstContactSuccess.IsFirstContact = true
	if err := c.Record(firstContactSuccess); err != nil {
		t.Fatalf("record first-contact success: %v", err)
	}
	firstContactMiss := validRecord("first-miss", base.Add(2*time.Second))
	firstContactMiss.IsFirstContact = true
	firstContactMiss.StartedAt = time.Time{}
	firstContactMiss.CompletedAt = time.Time{}
	firstContactMiss.TotalLatency = 0
	firstContactMiss.Outcome = "deadline_missed"
	firstContactMiss.MetDeadline = false
	if err := c.Record(firstContactMiss); err != nil {
		t.Fatalf("record first-contact miss: %v", err)
	}
	establishedSuccess := validRecord("established-success", base.Add(3*time.Second))
	if err := c.Record(establishedSuccess); err != nil {
		t.Fatalf("record established success: %v", err)
	}

	summary := c.ComputeSummary()
	if summary.FirstContactOffered != 2 ||
		summary.LegitimateFirstContactOffered != 2 ||
		summary.LegitimateFirstContactSucceeded != 1 ||
		summary.LegitimateFirstContactSuccessRate != 0.5 {
		t.Fatalf("first-contact summary = %+v, want 2 offered, 1 success, S_F=0.5", summary)
	}
	if summary.LegitimateDeadlineSuccessRate != 2.0/3.0 {
		t.Fatalf("overall legitimate success rate = %v, want 2/3", summary.LegitimateDeadlineSuccessRate)
	}
}

func TestRecordIDsAreIdempotentAndConflictsRejected(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := NewCollector("baseline", "run-1", 1)
	record := validRecord("request-1", base)
	if err := c.Record(record); err != nil {
		t.Fatalf("record initial request: %v", err)
	}
	if err := c.Record(record); err != nil {
		t.Fatalf("retry identical record: %v", err)
	}
	conflict := record
	conflict.IsLegitimate = false
	if err := c.Record(conflict); err == nil {
		t.Fatal("accepted conflicting duplicate request ID")
	}
	if got := c.RecordCount(); got != 1 {
		t.Fatalf("record count = %d, want 1", got)
	}
}

func TestRecordValidatesSuccessFields(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := NewCollector("rescue", "run-1", 1)
	record := validRecord("late", base)
	record.CompletedAt = record.Deadline.Add(time.Nanosecond)
	record.TotalLatency = record.CompletedAt.Sub(record.ArrivedAt)
	if err := c.Record(record); err == nil {
		t.Fatal("accepted late completion as a completed success")
	}
	record = validRecord("bad-latency", base)
	record.TotalLatency++
	if err := c.Record(record); err == nil {
		t.Fatal("accepted latency inconsistent with timestamps")
	}
}

func TestComputeSummaryUsesOneConsistentRecordSnapshot(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := NewCollector("rescue", "run-1", 1)
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; i < 500; i++ {
			record := validRecord(fmt.Sprintf("request-%04d", i), base.Add(time.Duration(i)*time.Second))
			if i%2 == 1 {
				record.Outcome = "rejected"
				record.WasAdmitted = false
				record.WasRejected = true
				record.AdmittedAt = time.Time{}
				record.StartedAt = time.Time{}
				record.CompletedAt = time.Time{}
				record.TotalLatency = 0
				record.MetDeadline = false
			}
			if err := c.Record(record); err != nil {
				t.Errorf("record request: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 100; i++ {
		summary := c.ComputeSummary()
		if summary.TotalRequests != summary.LegitimateOffered {
			t.Fatalf("summary mixed request snapshots: %+v", summary)
		}
		if summary.LegitimateOffered > 0 {
			wantSuccessRate := float64(summary.LegitimateSucceeded) / float64(summary.LegitimateOffered)
			wantRejectionRate := float64(summary.LegitimateRejected) / float64(summary.LegitimateOffered)
			if summary.LegitimateDeadlineSuccessRate != wantSuccessRate || summary.FalseRejectionRate != wantRejectionRate {
				t.Fatalf("summary rates do not match same counts: %+v", summary)
			}
		}
	}
	writer.Wait()
}
