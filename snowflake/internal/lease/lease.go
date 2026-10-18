// Package lease keeps a node ID lease alive by renewing it in the background.
package lease

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"time"
)

// ErrNotOwner is returned by a RenewFunc when the lease now belongs to someone else.
var ErrNotOwner = errors.New("lease: no longer the owner")

// RenewFunc extends the lease by its TTL.
type RenewFunc func(ctx context.Context) error

// Keeper renews a lease every TTL/3 and reports when it is lost.
type Keeper struct {
	ttl      time.Duration
	renew    RenewFunc
	lost     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// Start renews a lease acquired at acquiredAt that lasts ttl, until Stop is called.
// acquiredAt should be taken before the acquire request was sent.
func Start(acquiredAt time.Time, ttl time.Duration, renew RenewFunc) *Keeper {
	k := &Keeper{
		ttl:   ttl,
		renew: renew,
		lost:  make(chan struct{}),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go k.run(acquiredAt.Add(ttl))
	return k
}

// Lost is closed when the lease was taken by someone else, or when renewals kept
// failing and the lease is about to expire.
func (k *Keeper) Lost() <-chan struct{} {
	return k.lost
}

// Stop stops renewing the lease and waits for an in-flight renewal to finish.
func (k *Keeper) Stop() {
	k.stopOnce.Do(func() { close(k.stop) })
	<-k.done
}

func (k *Keeper) run(deadline time.Time) {
	defer close(k.done)

	interval := k.ttl / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-k.stop:
			return
		case <-ticker.C:
		}

		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), interval)
		err := k.renew(ctx)
		cancel()

		if err == nil {
			deadline = start.Add(k.ttl)
			continue
		}
		// Give up if someone else owns the lease, or if the lease could expire
		// before the next renewal attempt.
		if errors.Is(err, ErrNotOwner) || time.Until(deadline) < interval {
			close(k.lost)
			return
		}
	}
}

// NewOwner returns an identifier for this process, unique across hosts and restarts.
// It is at most 81 characters long.
func NewOwner() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	if len(host) > 64 {
		host = host[:64]
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return host + "-" + hex.EncodeToString(b)
}
