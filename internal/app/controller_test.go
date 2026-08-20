package app

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwhite616/sundial/internal/clock"
	"github.com/cwhite616/sundial/internal/render"
)

type testSystemClock struct {
	mu      sync.Mutex
	samples []clock.Sample
	calls   int
}

func (c *testSystemClock) Sample() clock.Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.samples[c.calls]
	c.calls++
	return v
}

type frameCollector struct {
	mu     sync.Mutex
	frames []render.Frame
}

type immediateStore struct{}

func (immediateStore) Save(context.Context, DeviceState) error { return nil }

type failingSubmitter struct{ err error }

func (s *failingSubmitter) Submit(render.Frame) error { return s.err }

type nilClock struct{}

func (*nilClock) Sample() clock.Sample { return clock.Sample{} }

type synchronizationSourceFunc func(context.Context) (SynchronizationObservation, error)

func (f synchronizationSourceFunc) Observe(ctx context.Context) (SynchronizationObservation, error) {
	return f(ctx)
}

type synchronizationDiagnostics struct {
	mu      sync.Mutex
	records []SynchronizationDiagnostic
}

func (d *synchronizationDiagnostics) RecordSynchronization(record SynchronizationDiagnostic) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, record)
}

func TestSynchronizationRefreshTransitionsAndTicksContinue(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	failure := errors.New("status unavailable")
	var mu sync.Mutex
	results := []struct {
		observation SynchronizationObservation
		err         error
	}{
		{err: failure},
		{observation: SynchronizationObservation{Classification: SynchronizationSynchronized, ObservedAt: observedAt}},
		{err: failure},
		{observation: SynchronizationObservation{Classification: SynchronizationUnsynchronized, ObservedAt: observedAt.Add(time.Minute)}},
	}
	source := synchronizationSourceFunc(func(context.Context) (SynchronizationObservation, error) {
		mu.Lock()
		defer mu.Unlock()
		result := results[0]
		results = results[1:]
		return result.observation, result.err
	})
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	frames := &frameCollector{}
	diagnostics := &synchronizationDiagnostics{}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{
		Clock: &testSystemClock{samples: []clock.Sample{
			{Wall: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)},
			{Wall: time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)},
		}}, Renderer: renderer, Frames: frames, Synchronization: source, Diagnostics: diagnostics,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	initial, _ := c.RuntimeSnapshot(context.Background())
	if initial.Synchronization.Classification != SynchronizationUnknown || initial.Synchronization.LastKnown != nil {
		t.Fatalf("initial health = %+v", initial.Synchronization)
	}
	if err := c.RefreshSynchronization(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("first failure = %v", err)
	}
	assertSynchronization(t, c, SynchronizationUnavailable, nil)
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshSynchronization(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSynchronization(t, c, SynchronizationSynchronized, &observedAt)
	if err := c.RefreshSynchronization(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("later failure = %v", err)
	}
	assertSynchronization(t, c, SynchronizationStale, &observedAt)
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshSynchronization(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRecovered := observedAt.Add(time.Minute)
	assertSynchronization(t, c, SynchronizationUnsynchronized, &wantRecovered)
	frames.mu.Lock()
	frameCount := len(frames.frames)
	frames.mu.Unlock()
	if frameCount != 2 {
		t.Fatalf("frames after degraded health = %d, want 2", frameCount)
	}
	waitForSynchronizationDiagnostics(t, diagnostics, 4)
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	if len(diagnostics.records) != 4 {
		t.Fatalf("diagnostic count = %d", len(diagnostics.records))
	}
	for _, record := range diagnostics.records {
		if record.Operation == "" || record.Classification == "" {
			t.Fatalf("unstable diagnostic = %+v", record)
		}
	}
}

func waitForSynchronizationDiagnostics(t *testing.T, diagnostics *synchronizationDiagnostics, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		diagnostics.mu.Lock()
		count := len(diagnostics.records)
		diagnostics.mu.Unlock()
		if count >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("diagnostic count = %d, want at least %d", count, want)
		}
		time.Sleep(time.Millisecond)
	}
}

type blockingSynchronizationDiagnostics struct {
	entered chan struct{}
	block   chan struct{}
}

type orderedSynchronizationDiagnostics struct {
	mu         sync.Mutex
	records    []SynchronizationDiagnostic
	active     int
	maxActive  int
	firstDelay chan struct{}
}

func (d *orderedSynchronizationDiagnostics) RecordSynchronization(record SynchronizationDiagnostic) {
	d.mu.Lock()
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	index := len(d.records)
	d.mu.Unlock()
	if index == 0 && d.firstDelay != nil {
		<-d.firstDelay
	}
	d.mu.Lock()
	d.records = append(d.records, record)
	d.active--
	d.mu.Unlock()
}

func (d *blockingSynchronizationDiagnostics) RecordSynchronization(SynchronizationDiagnostic) {
	select {
	case d.entered <- struct{}{}:
	default:
	}
	<-d.block
}

func TestBlockingSynchronizationDiagnosticsDoNotBlockController(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	diagnostics := &blockingSynchronizationDiagnostics{entered: make(chan struct{}, 1), block: make(chan struct{})}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{
		Clock:    &testSystemClock{samples: []clock.Sample{{Wall: time.Date(2026, 8, 20, 6, 0, 0, 0, time.UTC)}}},
		Renderer: renderer, Frames: &frameCollector{}, Diagnostics: diagnostics,
		Synchronization: synchronizationSourceFunc(func(context.Context) (SynchronizationObservation, error) {
			return SynchronizationObservation{Classification: SynchronizationSynchronized, ObservedAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer close(diagnostics.block)
	if err := c.RefreshSynchronization(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-diagnostics.entered:
	case <-time.After(time.Second):
		t.Fatal("diagnostic recorder was not called")
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatalf("tick blocked by diagnostic: %v", err)
	}
	if _, err := c.RuntimeSnapshot(context.Background()); err != nil {
		t.Fatalf("query blocked by diagnostic: %v", err)
	}
}

func TestSynchronizationDiagnosticsAreSerializedAndDrainedOnClose(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	diagnostics := &orderedSynchronizationDiagnostics{firstDelay: make(chan struct{})}
	observations := make(chan SynchronizationObservation, 2)
	firstAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	observations <- SynchronizationObservation{Classification: SynchronizationSynchronized, ObservedAt: firstAt}
	observations <- SynchronizationObservation{Classification: SynchronizationUnsynchronized, ObservedAt: firstAt.Add(time.Minute)}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{
		Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}, Diagnostics: diagnostics,
		Synchronization: synchronizationSourceFunc(func(context.Context) (SynchronizationObservation, error) {
			return <-observations, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshSynchronization(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshSynchronization(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(diagnostics.firstDelay)
	c.Close()
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	if diagnostics.maxActive != 1 {
		t.Fatalf("concurrent diagnostic calls = %d", diagnostics.maxActive)
	}
	if len(diagnostics.records) != 2 || diagnostics.records[0].Classification != SynchronizationSynchronized || diagnostics.records[1].Classification != SynchronizationUnsynchronized {
		t.Fatalf("diagnostic order = %+v", diagnostics.records)
	}
}

func TestCanceledRefreshCannotAdoptSourceSuccess(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	entered := make(chan struct{})
	diagnostics := &synchronizationDiagnostics{}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{
		Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}, Diagnostics: diagnostics,
		Synchronization: synchronizationSourceFunc(func(ctx context.Context) (SynchronizationObservation, error) {
			close(entered)
			<-ctx.Done()
			return SynchronizationObservation{Classification: SynchronizationSynchronized, ObservedAt: time.Now()}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- c.RefreshSynchronization(ctx) }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh cancellation = %v", err)
	}
	waitForSynchronizationDiagnostics(t, diagnostics, 1)
	assertSynchronization(t, c, SynchronizationUnavailable, nil)
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	if diagnostics.records[0].Classification != SynchronizationUnavailable || !strings.Contains(diagnostics.records[0].Error, context.Canceled.Error()) {
		t.Fatalf("canceled refresh diagnostic = %+v", diagnostics.records[0])
	}
}

func TestInvalidSynchronizationObservationsDegradeAndRetainLastKnown(t *testing.T) {
	validAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	results := []SynchronizationObservation{
		{Classification: SynchronizationClassification("invalid"), ObservedAt: validAt.Add(-time.Minute)},
		{Classification: SynchronizationSynchronized, ObservedAt: validAt},
		{Classification: SynchronizationClassification("invalid"), ObservedAt: validAt.Add(time.Minute)},
	}
	var mu sync.Mutex
	source := synchronizationSourceFunc(func(context.Context) (SynchronizationObservation, error) {
		mu.Lock()
		defer mu.Unlock()
		result := results[0]
		results = results[1:]
		return result, nil
	})
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	diagnostics := &synchronizationDiagnostics{}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}, Synchronization: source, Diagnostics: diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.RefreshSynchronization(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid synchronization classification") {
		t.Fatalf("invalid initial observation error = %v", err)
	}
	assertSynchronization(t, c, SynchronizationUnavailable, nil)
	if err := c.RefreshSynchronization(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshSynchronization(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid synchronization classification") {
		t.Fatalf("invalid later observation error = %v", err)
	}
	assertSynchronization(t, c, SynchronizationStale, &validAt)
	waitForSynchronizationDiagnostics(t, diagnostics, 3)
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	var unavailable, stale *SynchronizationDiagnostic
	for i := range diagnostics.records {
		record := &diagnostics.records[i]
		switch record.Classification {
		case SynchronizationUnavailable:
			unavailable = record
		case SynchronizationStale:
			stale = record
		}
	}
	if unavailable == nil || unavailable.Error == "" {
		t.Fatalf("missing initial invalid diagnostic: %+v", diagnostics.records)
	}
	if stale == nil || stale.Observation != validAt || stale.Error == "" {
		t.Fatalf("missing later invalid diagnostic: %+v", diagnostics.records)
	}
}

func TestZeroSynchronizationObservationTimeIsRejected(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}, Synchronization: synchronizationSourceFunc(func(context.Context) (SynchronizationObservation, error) {
		return SynchronizationObservation{Classification: SynchronizationUnknown}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.RefreshSynchronization(context.Background()); err == nil || !strings.Contains(err.Error(), "zero observation time") {
		t.Fatalf("zero observation error = %v", err)
	}
	assertSynchronization(t, c, SynchronizationUnavailable, nil)
}

func TestQueuedSynchronizationCancellationIsSkippedAndCloseCancelsInFlightObservation(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	canceled := make(chan struct{}, 1)
	var callsMu sync.Mutex
	calls := 0
	source := synchronizationSourceFunc(func(ctx context.Context) (SynchronizationObservation, error) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		entered <- struct{}{}
		if call > 1 {
			<-ctx.Done()
			canceled <- struct{}{}
			return SynchronizationObservation{}, ctx.Err()
		}
		select {
		case <-release:
			return SynchronizationObservation{Classification: SynchronizationSynchronized, ObservedAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}, nil
		case <-ctx.Done():
			canceled <- struct{}{}
			return SynchronizationObservation{}, ctx.Err()
		}
	})
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}, Synchronization: source})
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	go func() { firstResult <- c.RefreshSynchronization(context.Background()) }()
	<-entered

	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	cancelQueued()
	queued := synchronizationRequest{ctx: queuedCtx, result: make(chan error, 1)}
	c.synchronization <- queued
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if err := <-queued.result; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation = %v", err)
	}
	callsMu.Lock()
	if calls != 1 {
		t.Fatalf("source calls = %d, canceled queued request was executed", calls)
	}
	callsMu.Unlock()

	secondResult := make(chan error, 1)
	go func() { secondResult <- c.RefreshSynchronization(context.Background()) }()
	<-entered
	c.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("controller close did not cancel in-flight observation")
	}
}

func assertSynchronization(t *testing.T, c *Controller, classification SynchronizationClassification, observedAt *time.Time) {
	t.Helper()
	snapshot, err := c.RuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Synchronization.Classification != classification {
		t.Fatalf("health = %+v, want %s", snapshot.Synchronization, classification)
	}
	if observedAt == nil {
		if snapshot.Synchronization.LastKnown != nil {
			t.Fatalf("unexpected last known = %+v", snapshot.Synchronization.LastKnown)
		}
		return
	}
	if snapshot.Synchronization.LastKnown == nil || snapshot.Synchronization.LastKnown.ObservedAt != *observedAt {
		t.Fatalf("last known = %+v, want %v", snapshot.Synchronization.LastKnown, *observedAt)
	}
	snapshot.Synchronization.LastKnown.Classification = SynchronizationUnknown
	again, _ := c.RuntimeSnapshot(context.Background())
	if again.Synchronization.LastKnown.Classification == SynchronizationUnknown {
		t.Fatal("health snapshot aliases controller state")
	}
}

func TestConcurrentSynchronizationRefreshTickAndRuntimeQueryTraffic(t *testing.T) {
	const operations = 32
	state := mustState(t, 0, "UTC", 0, 9)
	samples := make([]clock.Sample, operations)
	for i := range samples {
		samples[i] = clock.Sample{
			Wall:      time.Date(2026, 8, 20, i%24, 0, 0, 0, time.UTC),
			Monotonic: time.Duration(i) * time.Second,
		}
	}
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	var sourceMu sync.Mutex
	sourceCalls := 0
	source := synchronizationSourceFunc(func(ctx context.Context) (SynchronizationObservation, error) {
		if err := ctx.Err(); err != nil {
			return SynchronizationObservation{}, err
		}
		sourceMu.Lock()
		call := sourceCalls
		sourceCalls++
		sourceMu.Unlock()
		return SynchronizationObservation{
			Classification: SynchronizationSynchronized,
			ObservedAt:     time.Date(2026, 8, 20, 12, 0, call, 0, time.UTC),
		}, nil
	})
	controller, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{
		Clock:           &testSystemClock{samples: samples},
		Renderer:        renderer,
		Frames:          &frameCollector{},
		Sun:             render.ArtificialSun{Color: render.Pixel{W: 255}, IntensityProfile: []uint8{255}},
		Synchronization: source,
		Diagnostics:     &synchronizationDiagnostics{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	start := make(chan struct{})
	errs := make(chan error, operations*3)
	var wg sync.WaitGroup
	for kind := 0; kind < 3; kind++ {
		kind := kind
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range operations {
				switch kind {
				case 0:
					errs <- controller.RefreshSynchronization(context.Background())
				case 1:
					errs <- controller.Tick(context.Background())
				case 2:
					snapshot, err := controller.RuntimeSnapshot(context.Background())
					if err == nil && snapshot.Synchronization.LastKnown != nil {
						snapshot.Synchronization.LastKnown.Classification = SynchronizationUnknown
					}
					errs <- err
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	sourceMu.Lock()
	calls := sourceCalls
	sourceMu.Unlock()
	if calls != operations {
		t.Fatalf("synchronization calls = %d, want %d", calls, operations)
	}
	snapshot, err := controller.RuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantObserved := time.Date(2026, 8, 20, 12, 0, operations-1, 0, time.UTC)
	if snapshot.Synchronization.Classification != SynchronizationSynchronized || snapshot.Synchronization.LastKnown == nil || snapshot.Synchronization.LastKnown.ObservedAt != wantObserved {
		t.Fatalf("final synchronization health = %+v, want last observation %v", snapshot.Synchronization, wantObserved)
	}
	if snapshot.Position < 0 || snapshot.Frame.Len() != 10 {
		t.Fatalf("runtime did not continue through concurrent refreshes: %+v", snapshot)
	}
}

func (s *frameCollector) Submit(frame render.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, render.NewFrame(frame.Pixels()))
	return nil
}

func TestRuntimeControllerDefaultsAutoSamplesPerTickAndDeliversBoundaries(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	store := newControlledStore()
	systemClock := &testSystemClock{samples: []clock.Sample{
		{Wall: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)},
		{Wall: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)},
		{Wall: time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)},
	}}
	renderer, err := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	if err != nil {
		t.Fatal(err)
	}
	frames := &frameCollector{}
	controller, err := NewRuntimeController(context.Background(), state, store, RuntimeOptions{Clock: systemClock, Renderer: renderer, Frames: frames, Sun: render.ArtificialSun{Color: render.Pixel{W: 255}, IntensityProfile: []uint8{255}}})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	before, err := controller.RuntimeSnapshot(context.Background())
	if err != nil || before.Mode != clock.TimelineAuto {
		t.Fatalf("initial runtime = %+v, %v", before, err)
	}
	for range 3 {
		if err := controller.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if systemClock.calls != 3 {
		t.Fatalf("clock calls = %d", systemClock.calls)
	}
	frames.mu.Lock()
	count := len(frames.frames)
	frames.mu.Unlock()
	if count != 2 {
		t.Fatalf("delivered frames = %d, want 2 boundary changes", count)
	}
	if snapshot, _ := controller.RuntimeSnapshot(context.Background()); !snapshot.Effective.Equal(time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)) || snapshot.Position != 9 {
		t.Fatalf("final runtime = %+v", snapshot)
	}
	frames.mu.Lock()
	lit, _ := frames.frames[len(frames.frames)-1].Pixel(9)
	frames.mu.Unlock()
	if lit.W == 0 {
		t.Fatal("expected final frame lit at calibrated position 9")
	}

	snapshot, err := controller.RuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Sun.IntensityProfile[0] = 0
	pixels := snapshot.Frame.Pixels()
	pixels[0] = render.Pixel{R: 255}
	again, _ := controller.RuntimeSnapshot(context.Background())
	if again.Sun.IntensityProfile[0] != 255 || again.Frame.Pixels()[0].R != 0 {
		t.Fatal("runtime snapshot aliases controller state")
	}
}

func TestRuntimeControllerOwnsSunTemplateAndRejectsTypedNilDependencies(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	var typedNilClock *nilClock
	if _, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: typedNilClock, Renderer: renderer, Frames: &frameCollector{}}); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("typed nil clock error = %v", err)
	}
	var typedNilFrames *frameCollector
	if _, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{}, Renderer: renderer, Frames: typedNilFrames}); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("typed nil frames error = %v", err)
	}
	profile := []uint8{255}
	frames := &frameCollector{}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{samples: []clock.Sample{{Wall: time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)}}}, Renderer: renderer, Frames: frames, Sun: render.ArtificialSun{Color: render.Pixel{W: 255}, IntensityProfile: profile}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	profile[0] = 0
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames.mu.Lock()
	pixel, _ := frames.frames[0].Pixel(0)
	frames.mu.Unlock()
	if pixel.W != 255 {
		t.Fatalf("caller mutation changed rendered pixel: %+v", pixel)
	}
}

func TestRuntimeControllerRejectsRendererCalibrationLengthMismatch(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(9, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	if _, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}}); err == nil {
		t.Fatal("mismatched renderer and calibration lengths were accepted")
	}
}

func TestLegacyControllerRuntimeOperationsAreUnavailable(t *testing.T) {
	c, err := NewController(context.Background(), mustState(t, 0, "UTC", 0, 9), immediateStore{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.ReplaceTimeline(context.Background(), clock.NewAutoTimeline()); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("replace error = %v", err)
	}
	if _, err := c.RuntimeSnapshot(context.Background()); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("snapshot error = %v", err)
	}
	if err := c.Tick(context.Background()); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("tick error = %v", err)
	}
}

func TestControllerAcceleratedTickIgnoresWallCorrection(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	anchor := time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)
	system := &testSystemClock{samples: []clock.Sample{{Wall: anchor.Add(-8 * time.Hour), Monotonic: 12 * time.Second}}}
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	frames := &frameCollector{}
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: system, Renderer: renderer, Frames: frames, Sun: render.ArtificialSun{Color: render.Pixel{W: 255}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	timeline, _ := clock.NewAcceleratedTimelineFromSample(clock.Sample{Wall: anchor, Monotonic: 10 * time.Second}, anchor, 3600)
	if err := c.ReplaceTimeline(context.Background(), timeline); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := c.RuntimeSnapshot(context.Background())
	want := anchor.Add(2 * time.Hour)
	if !snapshot.Effective.Equal(want) || snapshot.Position != 2 {
		t.Fatalf("accelerated runtime = %+v, want effective %v position 2", snapshot, want)
	}
	pixel, _ := snapshot.Frame.Pixel(2)
	if pixel.W == 0 {
		t.Fatal("accelerated frame not lit at position 2")
	}
}

func TestTickFailuresKeepCoherentSnapshotAndControllerUsable(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	sample := clock.Sample{Wall: time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)}
	goodRenderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	for _, tc := range []struct {
		name   string
		sun    render.ArtificialSun
		frames FrameSubmitter
		want   error
	}{
		{"render", render.ArtificialSun{IntensityProfile: []uint8{1, 2}}, &frameCollector{}, nil},
		{"submit", render.ArtificialSun{}, &failingSubmitter{err: errors.New("sink failed")}, errors.New("sink failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{samples: []clock.Sample{sample}}, Renderer: goodRenderer, Frames: tc.frames, Sun: tc.sun})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			err = c.Tick(context.Background())
			if err == nil || (tc.want != nil && !strings.Contains(err.Error(), tc.want.Error())) {
				t.Fatalf("tick error = %v", err)
			}
			snapshot, snapErr := c.RuntimeSnapshot(context.Background())
			if snapErr != nil || !snapshot.Effective.IsZero() || snapshot.Position != -1 || snapshot.Frame.Len() != 0 {
				t.Fatalf("snapshot after failure = %+v, %v", snapshot, snapErr)
			}
			if _, snapErr = c.Snapshot(context.Background()); snapErr != nil {
				t.Fatalf("controller unusable: %v", snapErr)
			}
		})
	}
}

func TestInvalidTimelineReplacementPreservesAdoptedTimeline(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	controller, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{samples: []clock.Sample{{Wall: time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC)}}}, Renderer: renderer, Frames: &frameCollector{}})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if err := controller.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixed := clock.NewTimelineFixed(time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC))
	if err := controller.ReplaceTimeline(context.Background(), fixed); err != nil {
		t.Fatal(err)
	}
	cleared, _ := controller.RuntimeSnapshot(context.Background())
	if !cleared.Effective.IsZero() || cleared.Position != -1 || cleared.Frame.Len() != 0 {
		t.Fatalf("timeline replacement retained stale render state: %+v", cleared)
	}
	if err := controller.ReplaceTimeline(context.Background(), clock.Timeline{}); err == nil {
		t.Fatal("invalid timeline replacement succeeded")
	}
	snapshot, err := controller.RuntimeSnapshot(context.Background())
	if err != nil || snapshot.Mode != clock.TimelineFixed {
		t.Fatalf("runtime after rejection = %+v, %v", snapshot, err)
	}
}

func TestDurableAdoptionClearsRuntimeFieldsFromOldCalibration(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{samples: []clock.Sample{{Wall: time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)}}}, Renderer: renderer, Frames: &frameCollector{}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Replace(context.Background(), Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: testPoints(t, 1, 8)}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.RuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Device.PreferredZone() != "America/Detroit" || !snapshot.Effective.IsZero() || snapshot.Position != -1 || snapshot.Frame.Len() != 0 {
		t.Fatalf("runtime after durable adoption = %+v", snapshot)
	}
}

func TestRuntimeControllerRejectsDurableStripLengthChange(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	c, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{}, Renderer: renderer, Frames: &frameCollector{}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Replace(context.Background(), Replacement{PreferredZone: "UTC", StripLength: 11, Points: testPoints(t, 0, 10)}); err == nil {
		t.Fatal("runtime controller accepted a strip-length-changing replacement")
	}
	snapshot, err := c.Snapshot(context.Background())
	if err != nil || snapshot.Calibration().StripLength() != 10 || snapshot.Revision() != 0 {
		t.Fatalf("state changed after rejected replacement: %+v, %v", snapshot, err)
	}
}

func TestControllerConcurrentTrafficIsLinearizedAndSnapshotsAreImmutable(t *testing.T) {
	state := mustState(t, 0, "UTC", 0, 9)
	samples := make([]clock.Sample, 128)
	for i := range samples {
		samples[i] = clock.Sample{Wall: time.Date(2026, 8, 19, i%24, 0, 0, 0, time.UTC), Monotonic: time.Duration(i) * time.Second}
	}
	renderer, _ := render.New(10, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255})
	controller, err := NewRuntimeController(context.Background(), state, immediateStore{}, RuntimeOptions{Clock: &testSystemClock{samples: samples}, Renderer: renderer, Frames: &frameCollector{}, Sun: render.ArtificialSun{Color: render.Pixel{W: 255}, IntensityProfile: []uint8{255}}})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	errs := make(chan error, 128)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 12; i++ {
				switch worker {
				case 0:
					errs <- controller.Tick(context.Background())
				case 1:
					if i%2 == 0 {
						errs <- controller.ReplaceTimeline(context.Background(), clock.NewAutoTimeline())
					} else {
						errs <- controller.ReplaceTimeline(context.Background(), clock.NewTimelineFixed(time.Date(2026, 8, 19, i, 0, 0, 0, time.UTC)))
					}
				case 2:
					errs <- controller.Replace(context.Background(), Replacement{PreferredZone: "UTC", StripLength: 10, Points: testPoints(t, i%4, 9-i%4)})
				case 3:
					runtimeState, e := controller.RuntimeSnapshot(context.Background())
					if e == nil && len(runtimeState.Sun.IntensityProfile) > 0 {
						runtimeState.Sun.IntensityProfile[0] = 0
						_ = runtimeState.Frame.Pixels()
					}
					if e != nil {
						errs <- e
					} else {
						_, e = controller.Snapshot(context.Background())
						errs <- e
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	runtimeState, err := controller.RuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeState.Sun.IntensityProfile) > 0 && runtimeState.Sun.IntensityProfile[0] != 255 {
		t.Fatal("concurrent snapshot mutation reached controller")
	}
	durable, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if durable.Revision() != 12 {
		t.Fatalf("durable revision = %d, want 12", durable.Revision())
	}
}

type controlledStore struct {
	calls   chan DeviceState
	results chan error
	once    sync.Once
}

type nilStoreFunc func(context.Context, DeviceState) error

func (f nilStoreFunc) Save(ctx context.Context, state DeviceState) error { return f(ctx, state) }

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

func TestControllerSkipsCanceledQueuedReplacement(t *testing.T) {
	store := newControlledStore()
	controller, err := NewController(context.Background(), mustState(t, 0, "UTC", 1, 8), store)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	firstResult := replaceAsync(controller, Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: testPoints(t, 2, 7)})
	receiveCall(t, store)
	queuedContext, cancelQueued := context.WithCancel(context.Background())
	queuedResult := make(chan error, 1)
	go func() {
		queuedResult <- controller.Replace(queuedContext, Replacement{PreferredZone: "Europe/London", StripLength: 10, Points: testPoints(t, 3, 6)})
	}()
	assertNoCall(t, store)
	cancelQueued()
	if err := <-queuedResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued result = %v; want context cancellation", err)
	}
	store.results <- nil
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	assertNoCall(t, store)
	state, err := controller.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision() != 1 || state.PreferredZone() != "America/Detroit" {
		t.Fatalf("canceled queued state adopted: revision=%d zone=%q", state.Revision(), state.PreferredZone())
	}
}

func TestControllerRejectsRevisionExhaustionWithoutStoreCall(t *testing.T) {
	store := newControlledStore()
	controller, err := NewController(context.Background(), mustState(t, math.MaxUint64, "UTC", 1, 8), store)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if err := controller.Replace(context.Background(), Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: testPoints(t, 2, 7)}); !errors.Is(err, ErrRevisionExhausted) {
		t.Fatalf("replacement error = %v; want ErrRevisionExhausted", err)
	}
	assertNoCall(t, store)
	state, err := controller.Snapshot(context.Background())
	if err != nil || state.Revision() != math.MaxUint64 {
		t.Fatalf("snapshot = revision %d, error %v", state.Revision(), err)
	}
}

func TestControllerRejectsTypedNilStore(t *testing.T) {
	var store nilStoreFunc
	controller, err := NewController(context.Background(), mustState(t, 0, "UTC", 1, 8), store)
	if !errors.Is(err, ErrNilStore) || controller != nil {
		t.Fatalf("controller, error = %v, %v; want nil, ErrNilStore", controller, err)
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
