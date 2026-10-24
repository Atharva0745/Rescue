// Package queue implements a bounded FIFO queue with fair scheduling.
//
// Invariant: queue never exceeds configured capacity (architecture §5.1).
// Provides per-source fairness to prevent a single identity from monopolizing
// queue slots.
package queue

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Item represents a queued request awaiting worker capacity.
type Item struct {
	TaskID     string
	SourceID   string
	Priority   int // Higher = more urgent (emergency > normal)
	EnqueuedAt time.Time
	Deadline   time.Time
}

// Queue is a bounded, fair FIFO queue.
type Queue struct {
	mu       sync.Mutex
	items    []Item
	capacity int
	byTask   map[string]struct{}

	// Per-source tracking for fairness (architecture §4.2)
	perSource    map[string]int
	maxPerSource int

	// Metrics
	totalEnqueued  int64
	totalRejected  int64
	totalDequeued  int64
	totalCancelled int64
	totalExpired   int64
}

// NewQueue creates a bounded queue.
// maxPerSource limits how many items a single source can have queued.
// Set maxPerSource to 0 for no per-source limit.
func NewQueue(capacity int, maxPerSource int) *Queue {
	if capacity < 1 {
		panic("queue capacity must be at least 1")
	}
	if maxPerSource <= 0 {
		maxPerSource = capacity // No effective per-source limit
	}
	return &Queue{
		items:        make([]Item, 0, capacity),
		capacity:     capacity,
		byTask:       make(map[string]struct{}),
		perSource:    make(map[string]int),
		maxPerSource: maxPerSource,
	}
}

// Enqueue adds an item to the queue. Returns error if queue is full
// or per-source limit exceeded.
func (q *Queue) Enqueue(item Item) error {
	if strings.TrimSpace(item.TaskID) == "" || strings.TrimSpace(item.SourceID) == "" {
		return fmt.Errorf("task ID and source ID are required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.byTask[item.TaskID]; exists {
		q.totalRejected++
		return fmt.Errorf("task %s is already queued", item.TaskID)
	}

	// Hard invariant: queue never exceeds capacity
	if len(q.items) >= q.capacity {
		q.totalRejected++
		return fmt.Errorf("queue full (%d/%d)", len(q.items), q.capacity)
	}

	// Per-source fairness check
	if q.perSource[item.SourceID] >= q.maxPerSource {
		q.totalRejected++
		return fmt.Errorf("source %s at queue limit (%d/%d)",
			item.SourceID, q.perSource[item.SourceID], q.maxPerSource)
	}

	item.EnqueuedAt = time.Now()
	q.items = append(q.items, item)
	q.byTask[item.TaskID] = struct{}{}
	q.perSource[item.SourceID]++
	q.totalEnqueued++

	return nil
}

// Dequeue removes and returns the next item from the queue.
// Returns the item and true, or zero-value and false if queue is empty.
// Expired items (past deadline) are skipped and counted.
func (q *Queue) Dequeue() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()

	for len(q.items) > 0 {
		// Take from front (FIFO)
		item := q.items[0]
		q.items = q.items[1:]
		delete(q.byTask, item.TaskID)
		q.perSource[item.SourceID]--
		if q.perSource[item.SourceID] <= 0 {
			delete(q.perSource, item.SourceID)
		}

		// Skip expired items. Zero-value deadlines mean "no expiry".
		if !item.Deadline.IsZero() && !now.Before(item.Deadline) {
			q.totalExpired++
			continue
		}

		q.totalDequeued++
		return item, true
	}

	return Item{}, false
}

// Snapshot returns a copy of queued items in FIFO order.
func (q *Queue) Snapshot() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]Item(nil), q.items...)
}

// RemoveForDispatch removes a selected item and counts it as dispatched.
func (q *Queue) RemoveForDispatch(taskID string) (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.TaskID != taskID {
			continue
		}
		q.removeAtLocked(i)
		q.totalDequeued++
		return item, true
	}
	return Item{}, false
}

// Peek looks at the next item without removing it.
func (q *Queue) Peek() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return Item{}, false
	}
	return q.items[0], true
}

// Len returns the current queue length.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Capacity returns the maximum queue capacity.
func (q *Queue) Capacity() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.capacity
}

// IsFull returns whether the queue is at capacity.
func (q *Queue) IsFull() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) >= q.capacity
}

// Remove removes a specific task from the queue (e.g. on cancellation).
func (q *Queue) Remove(taskID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.items {
		if item.TaskID == taskID {
			q.removeAtLocked(i)
			q.totalCancelled++
			return true
		}
	}
	return false
}

func (q *Queue) removeAtLocked(index int) Item {
	item := q.items[index]
	copy(q.items[index:], q.items[index+1:])
	q.items[len(q.items)-1] = Item{}
	q.items = q.items[:len(q.items)-1]
	delete(q.byTask, item.TaskID)
	q.perSource[item.SourceID]--
	if q.perSource[item.SourceID] <= 0 {
		delete(q.perSource, item.SourceID)
	}
	return item
}

// PurgeExpired removes all items past their deadline.
// Returns the count of purged items.
func (q *Queue) PurgeExpired() int {
	return len(q.RemoveExpired(time.Now()))
}

// RemoveExpired removes and returns all items whose deadline has elapsed.
func (q *Queue) RemoveExpired(now time.Time) []Item {
	q.mu.Lock()
	defer q.mu.Unlock()

	expired := make([]Item, 0)
	alive := q.items[:0]

	for _, item := range q.items {
		if !item.Deadline.IsZero() && !now.Before(item.Deadline) {
			expired = append(expired, item)
			q.totalExpired++
			delete(q.byTask, item.TaskID)
			q.perSource[item.SourceID]--
			if q.perSource[item.SourceID] <= 0 {
				delete(q.perSource, item.SourceID)
			}
		} else {
			alive = append(alive, item)
		}
	}

	q.items = alive
	return expired
}

// SourceCount returns how many items a specific source has queued.
func (q *Queue) SourceCount(sourceID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.perSource[sourceID]
}

// Stats returns queue statistics.
type Stats struct {
	Length         int   `json:"length"`
	Capacity       int   `json:"capacity"`
	TotalEnqueued  int64 `json:"total_enqueued"`
	TotalRejected  int64 `json:"total_rejected"`
	TotalDequeued  int64 `json:"total_dequeued"`
	TotalCancelled int64 `json:"total_cancelled"`
	TotalExpired   int64 `json:"total_expired"`
}

func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{
		Length:         len(q.items),
		Capacity:       q.capacity,
		TotalEnqueued:  q.totalEnqueued,
		TotalRejected:  q.totalRejected,
		TotalDequeued:  q.totalDequeued,
		TotalCancelled: q.totalCancelled,
		TotalExpired:   q.totalExpired,
	}
}
