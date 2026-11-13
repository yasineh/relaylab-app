package replay

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimit is an in-process token bucket per user (D6: per API instance).
// The bucket holds a minute's allowance and refills continuously.
type RateLimit struct {
	mu        sync.Mutex
	perMinute int
	users     map[string]*rate.Limiter
}

// NewRateLimiter allows perMinute replay creations per user per minute
// (REPLAY_USER_RATE_PER_MINUTE).
func NewRateLimiter(perMinute int) *RateLimit {
	return &RateLimit{perMinute: perMinute, users: map[string]*rate.Limiter{}}
}

// Allow takes one creation for user, or reports how long to wait.
func (l *RateLimit) Allow(user string) (bool, time.Duration) {
	l.mu.Lock()
	lim, ok := l.users[user]
	if !ok {
		lim = rate.NewLimiter(rate.Every(time.Minute/time.Duration(l.perMinute)), l.perMinute)
		l.users[user] = lim
	}
	l.mu.Unlock()
	r := lim.Reserve()
	if d := r.Delay(); d > 0 {
		r.Cancel()
		return false, d
	}
	return true, 0
}
