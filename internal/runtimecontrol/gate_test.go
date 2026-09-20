package runtimecontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGateSerializesSameResourceButNotDifferentResources(t *testing.T) {
	gate := New(Limits{Global: 4, PerNode: 4, PerWorkspace: 4, QueueTimeout: time.Second})
	releaseFirst, _, err := gate.Acquire(t.Context(), Request{NodeID: "node", WorkspaceID: "site", ResourceKeys: []string{"file:a"}})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()

	sameAcquired := make(chan func(), 1)
	go func() {
		release, _, acquireErr := gate.Acquire(context.Background(), Request{NodeID: "node", WorkspaceID: "site", ResourceKeys: []string{"file:a"}})
		if acquireErr == nil {
			sameAcquired <- release
		}
	}()
	select {
	case release := <-sameAcquired:
		release()
		t.Fatal("same resource acquired before release")
	case <-time.After(40 * time.Millisecond):
	}

	differentRelease, _, err := gate.Acquire(t.Context(), Request{NodeID: "node", WorkspaceID: "site", ResourceKeys: []string{"file:b"}})
	if err != nil {
		t.Fatalf("different resource unexpectedly blocked: %v", err)
	}
	differentRelease()

	releaseFirst()
	select {
	case release := <-sameAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("same resource did not resume after release")
	}
}

func TestGateEnforcesWorkspaceLimit(t *testing.T) {
	gate := New(Limits{Global: 4, PerNode: 4, PerWorkspace: 1, QueueTimeout: time.Second})
	release, _, err := gate.Acquire(t.Context(), Request{NodeID: "node", WorkspaceID: "site"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	acquired := make(chan func(), 1)
	go func() {
		nextRelease, _, acquireErr := gate.Acquire(context.Background(), Request{NodeID: "node", WorkspaceID: "site"})
		if acquireErr == nil {
			acquired <- nextRelease
		}
	}()
	select {
	case nextRelease := <-acquired:
		nextRelease()
		t.Fatal("workspace limit was not enforced")
	case <-time.After(40 * time.Millisecond):
	}
	release()
	select {
	case nextRelease := <-acquired:
		nextRelease()
	case <-time.After(time.Second):
		t.Fatal("queued workspace call did not resume")
	}
}

func TestGateTimesOutQueue(t *testing.T) {
	gate := New(Limits{Global: 1, PerNode: 1, PerWorkspace: 1, QueueTimeout: 35 * time.Millisecond})
	release, _, err := gate.Acquire(t.Context(), Request{NodeID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, waited, err := gate.Acquire(t.Context(), Request{NodeID: "node"})
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("queue error=%v", err)
	}
	if waited < 25*time.Millisecond {
		t.Fatalf("queue returned too early: %v", waited)
	}
}

func TestGateSortsResourceKeysToAvoidDeadlock(t *testing.T) {
	gate := New(Limits{Global: 2, PerNode: 2, PerWorkspace: 2, QueueTimeout: time.Second})
	release, _, err := gate.Acquire(t.Context(), Request{ResourceKeys: []string{"route:b", "route:a"}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		nextRelease, _, acquireErr := gate.Acquire(context.Background(), Request{ResourceKeys: []string{"route:a", "route:b"}})
		if acquireErr == nil {
			nextRelease()
		}
		done <- acquireErr
	}()
	time.Sleep(30 * time.Millisecond)
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("multi-resource acquisition deadlocked")
	}
}
