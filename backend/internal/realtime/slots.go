package realtime

import "sync"

// Slots counts open streams per user on this instance. The cap is per
// instance, the same documented approximation as the ingestion rate limits.
type Slots struct {
	max  int
	mu   sync.Mutex
	open map[string]int
}

// NewSlots returns a counter allowing max concurrent streams per user
// (LIMIT_STREAMS_PER_USER).
func NewSlots(max int) *Slots {
	return &Slots{max: max, open: map[string]int{}}
}

// Acquire takes a slot for user. It returns false when the user is at the cap.
// Every successful Acquire must be paired with Release.
func (s *Slots) Acquire(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[user] >= s.max {
		return false
	}
	s.open[user]++
	return true
}

// Release frees a slot taken by Acquire.
func (s *Slots) Release(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[user] <= 1 {
		delete(s.open, user)
		return
	}
	s.open[user]--
}

// Open reports how many streams user holds (for tests and metrics).
func (s *Slots) Open(user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open[user]
}
