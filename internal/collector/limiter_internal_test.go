package collector

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Concurrent each calls sharing one Limiter never run more than its size.
func TestLimiter_BoundsAcrossCallers(t *testing.T) {
	const size = 3
	l := NewLimiter(size)
	var inFlight, peak, ran atomic.Int64
	work := func(int) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		ran.Add(1)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { l.each(context.Background(), 20, work) })
	}
	wg.Wait()
	if got := ran.Load(); got != 60 {
		t.Errorf("ran = %d, want 60", got)
	}
	if got := peak.Load(); got != size {
		t.Errorf("peak in flight = %d, want %d", got, size)
	}
}

// Once ctx is done, no new unit starts, with or without a Limiter.
func TestLimiter_CancelStartsNoNewWork(t *testing.T) {
	for name, l := range map[string]*Limiter{"nil": nil, "one": NewLimiter(1)} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ran []int
			l.each(ctx, 5, func(i int) {
				ran = append(ran, i)
				cancel()
			})
			if len(ran) != 1 || ran[0] != 0 {
				t.Errorf("ran = %v, want [0]", ran)
			}
		})
	}
}

func TestLimiter_SizeBelowOneIsOne(t *testing.T) {
	if got := cap(NewLimiter(0).slots); got != 1 {
		t.Errorf("cap = %d, want 1", got)
	}
}
