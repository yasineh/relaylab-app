// Package realtime fans out new-event notifications to live dashboard streams.
//
// Ingestion publishes pg_notify('events', {project_id, event_id}) after an
// event commits. Each api instance holds one dedicated LISTEN connection (a
// Source) and dispatches notifications in-process to subscribers keyed by
// project. Notifications carry ids only; streams load summaries from the
// database, scoped to their owner, so what they send is always DB truth.
package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SubscriberBuffer is how many undelivered event ids a subscriber may hold.
// A subscriber whose buffer is full is dropped with resync required, so a
// slow client never slows the broker or ingestion. It is part of the stream
// contract (live-event-stream spec), not an operator limit.
const SubscriberBuffer = 256

// DropReason says why the broker dropped a subscription.
type DropReason int

const (
	// DropSlow means the subscriber's buffer was full.
	DropSlow DropReason = iota + 1
	// DropReconnect means the LISTEN connection was (re)established, so
	// notifications may have been lost.
	DropReconnect
)

// Notification is one committed event, as published by ingestion.
type Notification struct {
	ProjectID uuid.UUID
	EventID   uuid.UUID
}

// Source delivers notifications from one listening connection. Listen calls
// connected once it is listening, then deliver for each notification, until
// ctx is done or the connection fails (it then returns the error).
type Source interface {
	Listen(ctx context.Context, connected func(), deliver func(Notification)) error
}

// Options configure a Broker. Zero values select the defaults.
type Options struct {
	Source Source
	Logger *slog.Logger
	// MinBackoff and MaxBackoff bound the reconnect delay after a failure.
	MinBackoff, MaxBackoff time.Duration
	// OnSlowDrop is called once for each subscriber dropped for a full buffer.
	OnSlowDrop func()
	// OnConnect is called each time the source starts listening.
	OnConnect func()
}

// Broker owns the subscriber registry. It is safe for concurrent use.
type Broker struct {
	o    Options
	mu   sync.Mutex
	subs map[uuid.UUID]map[*Subscription]struct{}
}

// NewBroker returns a broker. Call Run to start listening.
func NewBroker(o Options) *Broker {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = 250 * time.Millisecond
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = max(o.MinBackoff, 10*time.Second)
	}
	return &Broker{o: o, subs: map[uuid.UUID]map[*Subscription]struct{}{}}
}

// Run listens until ctx is done, reconnecting with exponential backoff. Every
// time the source (re)connects, all current subscribers are dropped with
// resync required: notifications sent while nobody was listening are lost,
// so their streams must recover from the database.
func (b *Broker) Run(ctx context.Context) {
	backoff := b.o.MinBackoff
	for {
		err := b.o.Source.Listen(ctx, func() {
			backoff = b.o.MinBackoff
			if n := b.dropAll(); n > 0 {
				b.o.Logger.Info("event listener connected; streams must resync", slog.Int("streams", n))
			}
			if b.o.OnConnect != nil {
				b.o.OnConnect()
			}
		}, b.dispatch)
		if ctx.Err() != nil {
			return
		}
		b.o.Logger.Warn("event listener disconnected", slog.String("error", errText(err)),
			slog.Duration("retry_in", backoff))
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		backoff = min(backoff*2, b.o.MaxBackoff)
	}
}

func errText(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

// Subscribe registers interest in a project's new events.
func (b *Broker) Subscribe(project uuid.UUID) *Subscription {
	s := &Subscription{
		b: b, project: project,
		ch:      make(chan uuid.UUID, SubscriberBuffer),
		dropped: make(chan struct{}),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.subs[project]
	if set == nil {
		set = map[*Subscription]struct{}{}
		b.subs[project] = set
	}
	set[s] = struct{}{}
	return s
}

// Subscribers reports the number of registered subscribers (for tests).
func (b *Broker) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, set := range b.subs {
		n += len(set)
	}
	return n
}

// dispatch hands an event id to the project's subscribers without blocking.
func (b *Broker) dispatch(n Notification) {
	var slow int
	b.mu.Lock()
	for s := range b.subs[n.ProjectID] {
		select {
		case s.ch <- n.EventID:
		default:
			b.removeLocked(s)
			s.drop(DropSlow)
			slow++
		}
	}
	b.mu.Unlock()
	for range slow {
		if b.o.OnSlowDrop != nil {
			b.o.OnSlowDrop()
		}
	}
}

// dropAll drops every subscriber with resync required and returns how many.
func (b *Broker) dropAll() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, set := range b.subs {
		for s := range set {
			s.drop(DropReconnect)
			n++
		}
	}
	b.subs = map[uuid.UUID]map[*Subscription]struct{}{}
	return n
}

func (b *Broker) removeLocked(s *Subscription) bool {
	set := b.subs[s.project]
	if _, ok := set[s]; !ok {
		return false
	}
	delete(set, s)
	if len(set) == 0 {
		delete(b.subs, s.project)
	}
	return true
}

// Subscription receives one project's new event ids.
type Subscription struct {
	b       *Broker
	project uuid.UUID
	ch      chan uuid.UUID
	dropped chan struct{}
	reason  DropReason // written before dropped is closed
}

// drop records why and closes dropped. Callers hold the broker lock and have
// removed s from the registry, so it runs at most once.
func (s *Subscription) drop(r DropReason) {
	s.reason = r
	close(s.dropped)
}

// Reason reports why the subscription was dropped. Read it only after
// Dropped is closed.
func (s *Subscription) Reason() DropReason { return s.reason }

// Events delivers event ids in notification order.
func (s *Subscription) Events() <-chan uuid.UUID { return s.ch }

// Dropped is closed when the broker drops this subscription (buffer full or
// listener reconnect). The stream must then tell the client to resync.
func (s *Subscription) Dropped() <-chan struct{} { return s.dropped }

// Close unregisters the subscription. It is safe to call more than once and
// after the broker dropped it.
func (s *Subscription) Close() {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.removeLocked(s)
}
