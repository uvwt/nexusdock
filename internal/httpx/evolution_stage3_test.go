package httpx

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/nexusdock/internal/settings"
)

func TestStage3SchedulerRunsImmediatelyAndWakeKeepsOriginalDeadline(t *testing.T) {
	base := time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC)
	var nowNanos atomic.Int64
	nowNanos.Store(base.UnixNano())
	now := func() time.Time { return time.Unix(0, nowNanos.Load()).UTC() }
	cfg := settings.RuntimeAIConfig{
		Stage3Enabled: true, Stage3Endpoint: "http://model.invalid", Stage3Model: "test", Stage3Interval: 2 * time.Hour,
	}
	server := &Server{aiCfg: cfg, stage3Wake: make(chan struct{}, 1)}
	runs := make(chan settings.RuntimeAIConfig, 8)
	delays := make(chan time.Duration, 8)
	ticks := make(chan time.Time, 8)
	newTimer := func(wait time.Duration) evolutionStage3Timer {
		delays <- wait
		return evolutionStage3Timer{c: ticks, stop: func() {}}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go server.evolutionStage3Loop(ctx, now, newTimer, func(_ context.Context, got settings.RuntimeAIConfig) { runs <- got })

	select {
	case <-runs:
	case <-time.After(time.Second):
		t.Fatal("Stage 3 did not run immediately on startup")
	}
	if wait := <-delays; wait != 2*time.Hour {
		t.Fatalf("initial wait = %s", wait)
	}
	nowNanos.Store(base.Add(30 * time.Minute).UnixNano())
	server.stage3Wake <- struct{}{}
	if wait := <-delays; wait != 90*time.Minute {
		t.Fatalf("wait after wake = %s", wait)
	}
	select {
	case <-runs:
		t.Fatal("config wake triggered an unintended immediate run")
	default:
	}
	ticks <- now()
	select {
	case <-runs:
	case <-time.After(time.Second):
		t.Fatal("scheduled Stage 3 run did not execute")
	}
}

func TestStage3SchedulerRunsImmediatelyWhenReenabled(t *testing.T) {
	cfg := settings.RuntimeAIConfig{
		Stage3Enabled: false, Stage3Endpoint: "http://model.invalid", Stage3Model: "test", Stage3Interval: 2 * time.Hour,
	}
	server := &Server{aiCfg: cfg, stage3Wake: make(chan struct{}, 1)}
	runs := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go server.evolutionStage3Loop(ctx, time.Now, func(time.Duration) evolutionStage3Timer {
		return evolutionStage3Timer{c: make(chan time.Time), stop: func() {}}
	}, func(context.Context, settings.RuntimeAIConfig) { runs <- struct{}{} })

	server.mu.Lock()
	server.aiCfg.Stage3Enabled = true
	server.mu.Unlock()
	server.stage3Wake <- struct{}{}
	select {
	case <-runs:
	case <-time.After(time.Second):
		t.Fatal("Stage 3 did not run immediately after being re-enabled")
	}
}
