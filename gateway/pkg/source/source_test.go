package source

import (
	"sync"
	"testing"

	"github.com/rescue-gateway/gateway/pkg/pool"
)

func TestReserveEnforcesSharedSourceGroupCap(t *testing.T) {
	r := NewRegistry(map[string]int{
		"group":  1,
		"asn":    10,
		"region": 10,
		"global": 10,
	})
	r.RegisterSource("device-a", "group-a", "asn-a", "region-a")
	r.RegisterSource("device-b", "group-a", "asn-b", "region-b")

	if err := r.Reserve("device-a", 1); err != nil {
		t.Fatalf("reserve for first source failed: %v", err)
	}
	if err := r.Reserve("device-b", 1); err == nil {
		t.Fatal("expected sources sharing a group to share the group cap")
	}
}

func TestReserveUsesASNAndRegionAggregate(t *testing.T) {
	r := NewRegistry(map[string]int{
		"group":  10,
		"asn":    1,
		"region": 10,
		"global": 10,
	})
	r.RegisterSource("device-a", "group-a", "asn-a", "region-a")
	r.RegisterSource("device-b", "group-b", "asn-a", "region-a")
	r.RegisterSource("device-c", "group-c", "asn-a", "region-b")

	if err := r.Reserve("device-a", 1); err != nil {
		t.Fatalf("reserve for first source failed: %v", err)
	}
	if err := r.Reserve("device-b", 1); err == nil {
		t.Fatal("expected sources sharing ASN and region to share the aggregate cap")
	}
	if err := r.Reserve("device-c", 1); err != nil {
		t.Fatalf("different ASN-region scope should have its own cap: %v", err)
	}
}

func TestConcurrentReservationsCannotExceedCap(t *testing.T) {
	r := NewRegistry(map[string]int{
		"group":  100,
		"asn":    100,
		"region": 100,
		"global": 1,
	})
	r.RegisterSource("device-a", "group-a", "asn-a", "region-a")
	r.RegisterSource("device-b", "group-b", "asn-b", "region-b")

	results := make(chan error, 2)
	go func() { results <- r.Reserve("device-a", 1) }()
	go func() { results <- r.Reserve("device-b", 1) }()

	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}

	if successes != 1 {
		t.Fatalf("expected exactly one reservation under global cap, got %d", successes)
	}
}

func TestChoosePoolIsSafeDuringPromotionAndPolicyReplacement(t *testing.T) {
	r := NewRegistry(nil)
	r.RegisterSource("source", "group", "asn", "region")
	r.SetPoolPolicy(pool.NewPolicy(8, 1))

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if worker == 0 {
					r.Promote("source", TierNormal, "normal")
					r.Promote("source", TierEmergency, "emergency")
				} else if worker == 1 {
					r.SetPoolPolicy(pool.NewPolicy(8, 1))
				} else {
					_ = r.ChoosePool("source", "normal")
					_ = r.Get("source")
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestReserveRejectsOverflowingRequest(t *testing.T) {
	r := NewRegistry(nil)
	r.RegisterSource("source", "group", "asn", "region")
	maxInt := int(^uint(0) >> 1)
	if err := r.Reserve("source", maxInt); err == nil {
		t.Fatal("reservation overflow bypassed quota caps")
	}
}
