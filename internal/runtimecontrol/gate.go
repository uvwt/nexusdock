package runtimecontrol

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrQueueTimeout = errors.New("Runtime tool queue wait timed out")

type Limits struct {
	Global       int
	PerNode      int
	PerWorkspace int
	QueueTimeout time.Duration
}

type Request struct {
	NodeID       string
	WorkspaceID  string
	ResourceKeys []string
}

type Gate struct {
	limits     Limits
	global     chan struct{}
	nodes      *keyedLimiter
	workspaces *keyedLimiter
	resources  *keyedLimiter
}

func New(limits Limits) *Gate {
	if limits.Global <= 0 {
		limits.Global = 16
	}
	if limits.PerNode <= 0 {
		limits.PerNode = 4
	}
	if limits.PerWorkspace <= 0 {
		limits.PerWorkspace = 3
	}
	if limits.QueueTimeout <= 0 {
		limits.QueueTimeout = 30 * time.Second
	}
	return &Gate{
		limits:     limits,
		global:     make(chan struct{}, limits.Global),
		nodes:      newKeyedLimiter(limits.PerNode),
		workspaces: newKeyedLimiter(limits.PerWorkspace),
		resources:  newKeyedLimiter(1),
	}
}

func (g *Gate) Limits() Limits {
	if g == nil {
		return Limits{}
	}
	return g.limits
}

func (g *Gate) Acquire(ctx context.Context, request Request) (func(), time.Duration, error) {
	if g == nil {
		return func() {}, 0, nil
	}
	started := time.Now()
	queueCtx, cancel := context.WithTimeout(ctx, g.limits.QueueTimeout)
	defer cancel()

	releases := make([]func(), 0, 3+len(request.ResourceKeys))
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}

	if err := acquireChannel(queueCtx, g.global); err != nil {
		return nil, time.Since(started), queueError(ctx, err)
	}
	releases = append(releases, func() { <-g.global })

	if nodeID := strings.TrimSpace(request.NodeID); nodeID != "" {
		release, err := g.nodes.acquire(queueCtx, nodeID)
		if err != nil {
			releaseAll()
			return nil, time.Since(started), queueError(ctx, err)
		}
		releases = append(releases, release)
	}
	if workspaceID := strings.TrimSpace(request.WorkspaceID); workspaceID != "" {
		release, err := g.workspaces.acquire(queueCtx, workspaceID)
		if err != nil {
			releaseAll()
			return nil, time.Since(started), queueError(ctx, err)
		}
		releases = append(releases, release)
	}
	for _, key := range normalizeKeys(request.ResourceKeys) {
		release, err := g.resources.acquire(queueCtx, key)
		if err != nil {
			releaseAll()
			return nil, time.Since(started), queueError(ctx, err)
		}
		releases = append(releases, release)
	}

	var once sync.Once
	return func() { once.Do(releaseAll) }, time.Since(started), nil
}

func queueError(parent context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrQueueTimeout
	}
	return err
}

func acquireChannel(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizeKeys(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	keys := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

type keyedLimiter struct {
	mu       sync.Mutex
	capacity int
	slots    map[string]*limiterSlot
}

type limiterSlot struct {
	sem  chan struct{}
	refs int
}

func newKeyedLimiter(capacity int) *keyedLimiter {
	if capacity < 1 {
		capacity = 1
	}
	return &keyedLimiter{capacity: capacity, slots: make(map[string]*limiterSlot)}
}

func (l *keyedLimiter) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	slot := l.slots[key]
	if slot == nil {
		slot = &limiterSlot{sem: make(chan struct{}, l.capacity)}
		l.slots[key] = slot
	}
	slot.refs++
	l.mu.Unlock()

	if err := acquireChannel(ctx, slot.sem); err != nil {
		l.releaseRef(key, slot, false)
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { l.releaseRef(key, slot, true) })
	}, nil
}

func (l *keyedLimiter) releaseRef(key string, slot *limiterSlot, acquired bool) {
	if acquired {
		<-slot.sem
	}
	l.mu.Lock()
	slot.refs--
	if slot.refs == 0 {
		delete(l.slots, key)
	}
	l.mu.Unlock()
}
