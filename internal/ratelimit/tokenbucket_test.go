package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestInMemoryAllowsUpToCapacity(t *testing.T) {
	rl := NewInMemory(3, 0.0001) // effectively no refill during the test

	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow("1.2.3.4"); !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if ok, _ := rl.Allow("1.2.3.4"); ok {
		t.Fatal("4th request should be rate limited")
	}
}

func TestInMemoryBucketsArePerIP(t *testing.T) {
	rl := NewInMemory(1, 0.0001)

	rl.Allow("1.1.1.1")
	if ok, _ := rl.Allow("1.1.1.1"); ok {
		t.Fatal("1.1.1.1 should be out of tokens")
	}
	if ok, _ := rl.Allow("2.2.2.2"); !ok {
		t.Fatal("2.2.2.2 has its own bucket and should be allowed")
	}
}

func TestInMemoryRefills(t *testing.T) {
	rl := NewInMemory(1, 100) // 1 token every 10ms

	rl.Allow("ip")
	if ok, _ := rl.Allow("ip"); ok {
		t.Fatal("bucket should be empty right after first request")
	}
	time.Sleep(30 * time.Millisecond)
	if ok, _ := rl.Allow("ip"); !ok {
		t.Fatal("bucket should have refilled")
	}
}

// Run with -race: many goroutines hammering one IP must never let more
// than capacity requests through.
func TestInMemoryConcurrentNeverExceedsCapacity(t *testing.T) {
	const capacity = 50
	rl := NewInMemory(capacity, 0.0001)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := rl.Allow("same-ip"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != capacity {
		t.Fatalf("allowed %d requests, want exactly %d", allowed, capacity)
	}
}
