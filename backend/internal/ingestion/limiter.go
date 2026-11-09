package ingestion

import (
	"container/list"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxLimiterKeys bounds each limiter map; the least recently used key is evicted.
const maxLimiterKeys = 10_000

// keyedLimiter holds one token bucket per key (endpoint id or client IP) in a
// bounded LRU, so memory stays fixed under floods of distinct keys. Limits are
// per process (design: Rate limiting).
type keyedLimiter struct {
	mu    sync.Mutex
	limit rate.Limit
	burst int
	max   int
	items map[string]*list.Element
	order *list.List // front = most recently used
}

type limiterEntry struct {
	key string
	lim *rate.Limiter
}

func newKeyedLimiter(perMinute, burst int) *keyedLimiter {
	return &keyedLimiter{
		limit: rate.Limit(float64(perMinute) / time.Minute.Seconds()),
		burst: burst,
		max:   maxLimiterKeys,
		items: map[string]*list.Element{},
		order: list.New(),
	}
}

// allow takes one token for key. When none is available it takes nothing and
// returns the whole seconds to wait (at least 1) for Retry-After.
func (k *keyedLimiter) allow(key string, now time.Time) (bool, int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var lim *rate.Limiter
	if el, ok := k.items[key]; ok {
		k.order.MoveToFront(el)
		lim = el.Value.(*limiterEntry).lim
	} else {
		lim = rate.NewLimiter(k.limit, k.burst)
		k.items[key] = k.order.PushFront(&limiterEntry{key: key, lim: lim})
		if k.order.Len() > k.max {
			oldest := k.order.Back()
			k.order.Remove(oldest)
			delete(k.items, oldest.Value.(*limiterEntry).key)
		}
	}
	res := lim.ReserveN(now, 1)
	if !res.OK() {
		return false, 1
	}
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now) // a rejected request does not consume capacity
		return false, int(math.Ceil(delay.Seconds()))
	}
	return true, 0
}

func (k *keyedLimiter) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.order.Len()
}
