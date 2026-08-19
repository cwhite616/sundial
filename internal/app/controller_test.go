package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type controlledStore struct {
	calls   chan DeviceState
	results chan error
	once    sync.Once
}

func newControlledStore() *controlledStore {
	return &controlledStore{calls: make(chan DeviceState, 8), results: make(chan error, 8)}
}

func (s *controlledStore) Save(ctx context.Context, state DeviceState) error {
	select {
	case s.calls <- state:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-s.results:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestControllerKeepsOldStateUntilPersistenceSucceeds(t *testing.T) {
	initial := mustState(t, 4, "UTC", 1, 8)
	store := newControlledStore()
	controller, err := NewController(context.Background(), initial, store)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	replacement := Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: testPoints(t, 2, 7)}
	result := replaceAsync(controller, replacement)
	candidate := receiveCall(t, store)
	if candidate.Revision() != 5 || candidate.PreferredZone() != "America/Detroit" {
		t.Fatalf("candidate = revision %d, zone %q", candidate.Revision(), candidate.PreferredZone())
	}
	before, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision() != 4 || before.PreferredZone() != "UTC" {
		t.Fatalf("state adopted before save: revision %d, zone %q", before.Revision(), before.PreferredZone())
	}
	store.results <- nil
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	after, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision() != 5 || after.PreferredZone() != "America/Detroit" {
		t.Fatalf("successful state not adopted: revision %d, zone %q", after.Revision(), after.PreferredZone())
	}
}

func TestControllerQueuesUnmaterializedCommandsFIFOAndRetainsStateOnFailure(t *testing.T) {
	initial := mustState(t, 0, "UTC", 1, 8)
	store := newControlledStore()
	controller, err := NewController(context.Background(), initial, store)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	firstResult := replaceAsync(controller, Replacement{PreferredZone: "America/New_York", StripLength: 10, Points: testPoints(t, 2, 7)})
	first := receiveCall(t, store)
	secondResult := replaceAsync(controller, Replacement{PreferredZone: "America/Los_Angeles", StripLength: 10, Points: testPoints(t, 3, 6)})
	assertNoCall(t, store)

	writeErr := errors.New("disk full")
	store.results <- writeErr
	if err := <-firstResult; !errors.Is(err, writeErr) {
		t.Fatalf("first result = %v; want disk error", err)
	}
	second := receiveCall(t, store)
	if first.Revision() != 1 || second.Revision() != 1 {
		t.Fatalf("failed write skipped revision: first=%d second=%d", first.Revision(), second.Revision())
	}
	if second.PreferredZone() != "America/Los_Angeles" {
		t.Fatalf("commands reordered: second zone = %q", second.PreferredZone())
	}
	store.results <- nil
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
	state, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision() != 1 || state.PreferredZone() != "America/Los_Angeles" {
		t.Fatalf("wrong adopted state: revision=%d zone=%q", state.Revision(), state.PreferredZone())
	}
}

func TestControllerRevalidatesQueuedInputAndContinues(t *testing.T) {
	store := newControlledStore()
	controller, err := NewController(context.Background(), mustState(t, 0, "UTC", 1, 8), store)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	firstResult := replaceAsync(controller, Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: testPoints(t, 2, 7)})
	receiveCall(t, store)
	invalidResult := replaceAsync(controller, Replacement{PreferredZone: "UTC", StripLength: 1, Points: testPoints(t, 0, 1)})
	store.results <- nil
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if err := <-invalidResult; err == nil {
		t.Fatal("invalid queued replacement succeeded")
	}
	thirdResult := replaceAsync(controller, Replacement{PreferredZone: "Europe/London", StripLength: 10, Points: testPoints(t, 4, 5)})
	third := receiveCall(t, store)
	if third.Revision() != 2 || third.PreferredZone() != "Europe/London" {
		t.Fatalf("third candidate = revision %d, zone %q", third.Revision(), third.PreferredZone())
	}
	store.results <- nil
	if err := <-thirdResult; err != nil {
		t.Fatal(err)
	}
}

func TestControllerCancellationDoesNotLeakBlockedCallersOrPartiallyAdopt(t *testing.T) {
	store := newControlledStore()
	controller, err := NewController(context.Background(), mustState(t, 0, "UTC", 1, 8), store)
	if err != nil {
		t.Fatal(err)
	}

	requestContext, cancelRequest := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- controller.Replace(requestContext, Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: testPoints(t, 2, 7)})
	}()
	receiveCall(t, store)
	cancelRequest()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller result = %v", err)
	}
	state, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision() != 0 {
		t.Fatalf("in-flight state partially adopted: revision %d", state.Revision())
	}

	closed := make(chan struct{})
	go func() { controller.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("controller close blocked")
	}
}

func mustState(t *testing.T, revision uint64, zone string, firstPixel, secondPixel int) DeviceState {
	t.Helper()
	state, err := NewDeviceState(revision, Replacement{PreferredZone: zone, StripLength: 10, Points: testPoints(t, firstPixel, secondPixel)})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func replaceAsync(controller *Controller, replacement Replacement) <-chan error {
	result := make(chan error, 1)
	go func() { result <- controller.Replace(context.Background(), replacement) }()
	return result
}

func receiveCall(t *testing.T, store *controlledStore) DeviceState {
	t.Helper()
	select {
	case state := <-store.calls:
		return state
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for store call")
		return DeviceState{}
	}
}

func assertNoCall(t *testing.T, store *controlledStore) {
	t.Helper()
	select {
	case state := <-store.calls:
		t.Fatalf("unexpected concurrent store call for revision %d", state.Revision())
	case <-time.After(25 * time.Millisecond):
	}
}
