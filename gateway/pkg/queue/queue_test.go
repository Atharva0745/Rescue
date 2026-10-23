package queue

import (
	"testing"
	"time"
)

func TestDequeueAllowsZeroDeadline(t *testing.T) {
	q := NewQueue(2, 1)

	if err := q.Enqueue(Item{TaskID: "t1", SourceID: "s1"}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	item, ok := q.Dequeue()
	if !ok {
		t.Fatal("expected item to dequeue")
	}
	if item.TaskID != "t1" {
		t.Fatalf("unexpected task id: %s", item.TaskID)
	}
	if q.Len() != 0 {
		t.Fatalf("queue should be empty, got length %d", q.Len())
	}
}

func TestPurgeExpiredSkipsZeroDeadline(t *testing.T) {
	q := NewQueue(2, 2)
	if err := q.Enqueue(Item{TaskID: "t1", SourceID: "s1"}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	if err := q.Enqueue(Item{TaskID: "t2", SourceID: "s1", Deadline: time.Now().Add(-time.Second)}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	purged := q.PurgeExpired()
	if purged != 1 {
		t.Fatalf("expected 1 expired item, got %d", purged)
	}
	if q.Len() != 1 {
		t.Fatalf("expected 1 item to remain, got %d", q.Len())
	}
}

func TestSnapshotAndRemoveForDispatchPreserveAccounting(t *testing.T) {
	q := NewQueue(4, 4)
	now := time.Now()
	for _, item := range []Item{
		{TaskID: "dispatch", SourceID: "source"},
		{TaskID: "cancel", SourceID: "source"},
		{TaskID: "expire", SourceID: "source", Deadline: now.Add(-time.Second)},
		{TaskID: "remain", SourceID: "source"},
	} {
		if err := q.Enqueue(item); err != nil {
			t.Fatalf("enqueue %s: %v", item.TaskID, err)
		}
	}

	snapshot := q.Snapshot()
	snapshot[0].TaskID = "mutated"
	if got := q.Snapshot()[0].TaskID; got != "dispatch" {
		t.Fatalf("snapshot mutation changed queue item: got %s", got)
	}

	if _, removed := q.RemoveForDispatch("already-cancelled"); removed {
		t.Fatal("removed a task that was not queued")
	}
	if stats := q.Stats(); stats.Length != 4 || stats.TotalDequeued != 0 {
		t.Fatalf("failed removal changed queue accounting: %+v", stats)
	}

	item, removed := q.RemoveForDispatch("dispatch")
	if !removed || item.TaskID != "dispatch" {
		t.Fatalf("remove for dispatch = %+v, %t", item, removed)
	}
	if !q.Remove("cancel") {
		t.Fatal("failed to remove cancelled task")
	}
	beforeStaleRemoval := q.Stats()
	if _, removed := q.RemoveForDispatch("cancel"); removed {
		t.Fatal("dispatch removed a task after cancellation")
	}
	if afterStaleRemoval := q.Stats(); afterStaleRemoval != beforeStaleRemoval {
		t.Fatalf("stale dispatch removal changed queue accounting: before=%+v after=%+v", beforeStaleRemoval, afterStaleRemoval)
	}
	expired := q.RemoveExpired(now)
	if len(expired) != 1 || expired[0].TaskID != "expire" {
		t.Fatalf("expired items = %+v, want expire", expired)
	}

	stats := q.Stats()
	if stats.TotalEnqueued != stats.TotalDequeued+stats.TotalCancelled+stats.TotalExpired+int64(stats.Length) {
		t.Fatalf("queue accounting identity failed: %+v", stats)
	}
	if stats.TotalEnqueued != 4 || stats.TotalDequeued != 1 || stats.TotalCancelled != 1 ||
		stats.TotalExpired != 1 || stats.Length != 1 {
		t.Fatalf("unexpected queue counters: %+v", stats)
	}
}
