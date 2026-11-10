package collector

import (
	"context"
	"errors"
	"sync"
)

// errNotStarted is the result of a unit of work that never ran because the
// collection was cancelled first.
var errNotStarted = errors.New("not collected: collection cancelled")

// Limiter bounds the units of collection work of one target that run at
// once. One Limiter is shared by all collectors of a target, so the bound
// holds across them. A nil *Limiter runs one unit at a time.
type Limiter struct {
	slots chan struct{}
}

// NewLimiter returns a Limiter running at most n units at once; n below 1
// means 1.
func NewLimiter(n int) *Limiter {
	return &Limiter{slots: make(chan struct{}, max(n, 1))}
}

// each runs fn(i) for every i in [0, n), each in its own goroutine once it
// holds a slot, and returns when every started fn has returned. Once ctx is
// done it starts no more; fn(i) for the unstarted i never runs. fn must
// only write state owned by i.
func (l *Limiter) each(ctx context.Context, n int, fn func(i int)) {
	if l == nil {
		for i := 0; i < n && ctx.Err() == nil; i++ {
			fn(i)
		}
		return
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for i := range n {
		select {
		case <-ctx.Done():
			return
		case l.slots <- struct{}{}:
		}
		if ctx.Err() != nil {
			<-l.slots
			return
		}
		wg.Add(1)
		go func() {
			defer func() {
				<-l.slots
				wg.Done()
			}()
			fn(i)
		}()
	}
}
