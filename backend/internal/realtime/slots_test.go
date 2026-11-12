package realtime

import (
	"sync"
	"testing"
)

func TestSlotsPerUser(t *testing.T) {
	s := NewSlots(2)
	for i := range 2 {
		if !s.Acquire("a") {
			t.Fatalf("slot %d refused", i+1)
		}
	}
	if s.Acquire("a") {
		t.Fatal("third allowed")
	}
	if !s.Acquire("b") {
		t.Fatal("other user refused")
	}
	s.Release("a")
	if !s.Acquire("a") {
		t.Fatal("freed slot not reusable")
	}
	s.Release("a")
	s.Release("a")
	s.Release("b")
	if len(s.open) != 0 {
		t.Fatalf("entries left: %v", s.open)
	}
}

func TestSlotsConcurrent(t *testing.T) {
	s := NewSlots(3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for range 50 {
		wg.Go(func() {
			if s.Acquire("u") {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if granted != 3 || s.Open("u") != 3 {
		t.Fatalf("granted %d, open %d", granted, s.Open("u"))
	}
}
