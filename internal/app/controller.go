package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/cwhite616/sundial/internal/clock"
	"github.com/cwhite616/sundial/internal/render"
)

var (
	ErrNilStore           = errors.New("device state store is nil")
	ErrControllerClosed   = errors.New("device state controller is closed")
	ErrRuntimeUnavailable = errors.New("controller runtime dependencies are unavailable")
)

type SystemClock interface{ Sample() clock.Sample }
type FrameSubmitter interface{ Submit(render.Frame) error }

type SynchronizationClassification string

const (
	SynchronizationUnknown        SynchronizationClassification = "unknown"
	SynchronizationSynchronized   SynchronizationClassification = "synchronized"
	SynchronizationUnsynchronized SynchronizationClassification = "unsynchronized"
	SynchronizationStale          SynchronizationClassification = "stale"
	SynchronizationUnavailable    SynchronizationClassification = "unavailable"
)

type SynchronizationObservation struct {
	Classification SynchronizationClassification
	ObservedAt     time.Time
}

type SynchronizationHealth struct {
	Classification SynchronizationClassification
	ObservedAt     time.Time
	LastKnown      *SynchronizationObservation
}

type SynchronizationDiagnostic struct {
	Operation      string
	Classification SynchronizationClassification
	Observation    time.Time
	Error          string
}

type SynchronizationSource interface {
	Observe(context.Context) (SynchronizationObservation, error)
}

type SynchronizationDiagnostics interface {
	RecordSynchronization(SynchronizationDiagnostic)
}

type RuntimeOptions struct {
	Clock           SystemClock
	Renderer        *render.Renderer
	Frames          FrameSubmitter
	Sun             render.ArtificialSun
	Synchronization SynchronizationSource
	Diagnostics     SynchronizationDiagnostics
}

type RuntimeSnapshot struct {
	Device          DeviceState
	Mode            clock.TimelineMode
	Effective       time.Time
	Position        int
	Sun             render.ArtificialSun
	Frame           render.Frame
	Synchronization SynchronizationHealth
}

// DeviceStateStore is owned by the application consumer. Save must return
// promptly after ctx is canceled and must not retain mutable state.
type DeviceStateStore interface {
	Save(context.Context, DeviceState) error
}

type replacementRequest struct {
	replacement Replacement
	result      chan error
	ctx         context.Context
}

type snapshotRequest struct{ result chan DeviceState }
type runtimeSnapshotRequest struct{ result chan RuntimeSnapshot }
type tickRequest struct {
	ctx    context.Context
	result chan error
}
type timelineRequest struct {
	ctx      context.Context
	timeline clock.Timeline
	result   chan error
}

type synchronizationRequest struct {
	ctx    context.Context
	result chan error
}

type synchronizationResult struct {
	request     synchronizationRequest
	observation SynchronizationObservation
	err         error
}

type persistenceResult struct {
	revision uint64
	err      error
}

type pendingReplacement struct {
	request   replacementRequest
	candidate DeviceState
}

type Controller struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	replace                 chan replacementRequest
	snapshot                chan snapshotRequest
	runtimeSnapshot         chan runtimeSnapshotRequest
	tick                    chan tickRequest
	timeline                chan timelineRequest
	synchronization         chan synchronizationRequest
	synchronizationComplete chan synchronizationResult
	complete                chan persistenceResult
	done                    chan struct{}
	closeOnce               sync.Once
	runtimeEnabled          bool
}

func NewController(parent context.Context, initial DeviceState, store DeviceStateStore) (*Controller, error) {
	c, err := newController(parent, initial, store)
	if err != nil {
		return nil, err
	}
	go c.run(initial, store, RuntimeOptions{})
	return c, nil
}

func NewRuntimeController(parent context.Context, initial DeviceState, store DeviceStateStore, runtime RuntimeOptions) (*Controller, error) {
	if isNilInterface(runtime.Clock) || runtime.Renderer == nil || isNilInterface(runtime.Frames) {
		return nil, fmt.Errorf("initialize runtime controller: %w", ErrRuntimeUnavailable)
	}
	if got, want := runtime.Renderer.StripLength(), initial.Calibration().StripLength(); got != want {
		return nil, fmt.Errorf("initialize runtime controller: renderer strip length %d does not match calibration strip length %d", got, want)
	}
	runtime.Sun.IntensityProfile = append([]uint8(nil), runtime.Sun.IntensityProfile...)
	c, err := newController(parent, initial, store)
	if err != nil {
		return nil, err
	}
	c.runtimeEnabled = true
	go c.run(initial, store, runtime)
	return c, nil
}

func newController(parent context.Context, initial DeviceState, store DeviceStateStore) (*Controller, error) {
	if parent == nil {
		return nil, errors.New("initialize device state controller: nil context")
	}
	if err := parent.Err(); err != nil {
		return nil, fmt.Errorf("initialize device state controller: %w", err)
	}
	if isNilStore(store) {
		return nil, ErrNilStore
	}
	if err := initial.validate(); err != nil {
		return nil, fmt.Errorf("initialize device state controller: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	return &Controller{ctx: ctx, cancel: cancel, replace: make(chan replacementRequest), snapshot: make(chan snapshotRequest), complete: make(chan persistenceResult, 1), runtimeSnapshot: make(chan runtimeSnapshotRequest), tick: make(chan tickRequest), timeline: make(chan timelineRequest), synchronization: make(chan synchronizationRequest), synchronizationComplete: make(chan synchronizationResult), done: make(chan struct{})}, nil
}

func (c *Controller) RefreshSynchronization(ctx context.Context) error {
	if ctx == nil {
		return errors.New("refresh synchronization health: nil context")
	}
	if !c.runtimeEnabled {
		return fmt.Errorf("refresh synchronization health: %w", ErrRuntimeUnavailable)
	}
	request := synchronizationRequest{ctx: ctx, result: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return fmt.Errorf("refresh synchronization health: %w", ctx.Err())
	case <-c.done:
		return ErrControllerClosed
	case c.synchronization <- request:
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for synchronization health refresh: %w", ctx.Err())
	case <-c.done:
		return ErrControllerClosed
	case err := <-request.result:
		return err
	}
}

func (c *Controller) Replace(ctx context.Context, replacement Replacement) error {
	if ctx == nil {
		return errors.New("replace device state: nil context")
	}
	replacement.Points = append([]clock.CalibrationPoint(nil), replacement.Points...)
	request := replacementRequest{replacement: replacement, result: make(chan error, 1), ctx: ctx}
	select {
	case <-ctx.Done():
		return fmt.Errorf("replace device state: %w", ctx.Err())
	case <-c.done:
		return ErrControllerClosed
	case c.replace <- request:
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for device state replacement: %w", ctx.Err())
	case <-c.done:
		select {
		case err := <-request.result:
			return err
		default:
			return ErrControllerClosed
		}
	case err := <-request.result:
		return err
	}
}

func (c *Controller) Snapshot(ctx context.Context) (DeviceState, error) {
	if ctx == nil {
		return DeviceState{}, errors.New("query device state: nil context")
	}
	request := snapshotRequest{result: make(chan DeviceState, 1)}
	select {
	case <-ctx.Done():
		return DeviceState{}, fmt.Errorf("query device state: %w", ctx.Err())
	case <-c.done:
		return DeviceState{}, ErrControllerClosed
	case c.snapshot <- request:
	}
	select {
	case <-ctx.Done():
		return DeviceState{}, fmt.Errorf("wait for device state snapshot: %w", ctx.Err())
	case <-c.done:
		return DeviceState{}, ErrControllerClosed
	case state := <-request.result:
		return state, nil
	}
}

func (c *Controller) ReplaceTimeline(ctx context.Context, timeline clock.Timeline) error {
	if ctx == nil {
		return errors.New("replace timeline: nil context")
	}
	if !c.runtimeEnabled {
		return fmt.Errorf("replace timeline: %w", ErrRuntimeUnavailable)
	}
	request := timelineRequest{ctx: ctx, timeline: timeline, result: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return fmt.Errorf("replace timeline: %w", ctx.Err())
	case <-c.done:
		return ErrControllerClosed
	case c.timeline <- request:
	}
	select {
	case <-c.done:
		return ErrControllerClosed
	case err := <-request.result:
		return err
	}
}

func (c *Controller) Tick(ctx context.Context) error {
	if ctx == nil {
		return errors.New("tick timeline: nil context")
	}
	request := tickRequest{ctx: ctx, result: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return fmt.Errorf("tick timeline: %w", ctx.Err())
	case <-c.done:
		return ErrControllerClosed
	case c.tick <- request:
	}
	select {
	case <-c.done:
		return ErrControllerClosed
	case err := <-request.result:
		return err
	}
}

func (c *Controller) RuntimeSnapshot(ctx context.Context) (RuntimeSnapshot, error) {
	if ctx == nil {
		return RuntimeSnapshot{}, errors.New("query runtime state: nil context")
	}
	if !c.runtimeEnabled {
		return RuntimeSnapshot{}, fmt.Errorf("query runtime state: %w", ErrRuntimeUnavailable)
	}
	request := runtimeSnapshotRequest{result: make(chan RuntimeSnapshot, 1)}
	select {
	case <-ctx.Done():
		return RuntimeSnapshot{}, fmt.Errorf("query runtime state: %w", ctx.Err())
	case <-c.done:
		return RuntimeSnapshot{}, ErrControllerClosed
	case c.runtimeSnapshot <- request:
	}
	select {
	case <-ctx.Done():
		return RuntimeSnapshot{}, fmt.Errorf("wait for runtime snapshot: %w", ctx.Err())
	case <-c.done:
		return RuntimeSnapshot{}, ErrControllerClosed
	case state := <-request.result:
		return cloneRuntimeSnapshot(state), nil
	}
}

func cloneRuntimeSnapshot(state RuntimeSnapshot) RuntimeSnapshot {
	state.Sun.IntensityProfile = append([]uint8(nil), state.Sun.IntensityProfile...)
	state.Frame = render.NewFrame(state.Frame.Pixels())
	if state.Synchronization.LastKnown != nil {
		last := *state.Synchronization.LastKnown
		state.Synchronization.LastKnown = &last
	}
	return state
}

func clearedRuntimeSnapshot(device DeviceState, mode clock.TimelineMode, template render.ArtificialSun) RuntimeSnapshot {
	template.Position = 0
	template.IntensityProfile = append([]uint8(nil), template.IntensityProfile...)
	return RuntimeSnapshot{Device: device, Mode: mode, Position: -1, Sun: template, Synchronization: SynchronizationHealth{Classification: SynchronizationUnknown}}
}

func (c *Controller) Close() {
	c.closeOnce.Do(c.cancel)
	<-c.done
}

func (c *Controller) run(adopted DeviceState, store DeviceStateStore, runtime RuntimeOptions) {
	defer close(c.done)
	timeline := clock.NewAutoTimeline()
	ownedSun := runtime.Sun
	ownedSun.IntensityProfile = append([]uint8(nil), runtime.Sun.IntensityProfile...)
	current := clearedRuntimeSnapshot(adopted, clock.TimelineAuto, ownedSun)
	health := SynchronizationHealth{Classification: SynchronizationUnknown}
	var queue []replacementRequest
	var inFlight *pendingReplacement
	var synchronizationQueue []synchronizationRequest
	var synchronizationInFlight bool

	startNext := func() {
		for inFlight == nil && len(queue) > 0 {
			request := queue[0]
			queue = queue[1:]
			if err := request.ctx.Err(); err != nil {
				request.result <- fmt.Errorf("replace queued device state: %w", err)
				continue
			}
			revision, err := nextRevision(adopted.Revision())
			if err != nil {
				request.result <- fmt.Errorf("replace device state: %w", err)
				continue
			}
			candidate, err := NewDeviceState(revision, request.replacement)
			if err != nil {
				request.result <- fmt.Errorf("replace device state: %w", err)
				continue
			}
			if c.runtimeEnabled && candidate.Calibration().StripLength() != runtime.Renderer.StripLength() {
				request.result <- fmt.Errorf("replace device state: calibration strip length %d does not match runtime renderer strip length %d", candidate.Calibration().StripLength(), runtime.Renderer.StripLength())
				continue
			}
			inFlight = &pendingReplacement{request: request, candidate: candidate}
			go func(state DeviceState) {
				err := store.Save(c.ctx, state)
				result := persistenceResult{revision: state.Revision(), err: err}
				select {
				case c.complete <- result:
				case <-c.ctx.Done():
				}
			}(candidate)
		}
	}
	startSynchronization := func() {
		if synchronizationInFlight {
			return
		}
		var request synchronizationRequest
		for len(synchronizationQueue) > 0 {
			request = synchronizationQueue[0]
			synchronizationQueue = synchronizationQueue[1:]
			if err := request.ctx.Err(); err != nil {
				request.result <- fmt.Errorf("refresh queued synchronization health: %w", err)
				continue
			}
			break
		}
		if request.ctx == nil {
			return
		}
		if isNilInterface(runtime.Synchronization) {
			request.result <- fmt.Errorf("refresh synchronization health: %w", ErrRuntimeUnavailable)
			return
		}
		synchronizationInFlight = true
		go func() {
			observeCtx, cancelObserve := context.WithCancel(c.ctx)
			stopRequestCancellation := context.AfterFunc(request.ctx, cancelObserve)
			defer cancelObserve()
			defer stopRequestCancellation()
			observation, err := runtime.Synchronization.Observe(observeCtx)
			result := synchronizationResult{request: request, observation: observation, err: err}
			select {
			case c.synchronizationComplete <- result:
			case <-c.ctx.Done():
			}
		}()
	}

	for {
		startNext()
		startSynchronization()
		select {
		case <-c.ctx.Done():
			err := fmt.Errorf("device state controller stopped: %w", c.ctx.Err())
			if inFlight != nil {
				inFlight.request.result <- err
			}
			for _, request := range queue {
				request.result <- err
			}
			return
		case request := <-c.replace:
			queue = append(queue, request)
		case request := <-c.snapshot:
			request.result <- adopted
		case request := <-c.runtimeSnapshot:
			current.Device = adopted
			current.Synchronization = health
			request.result <- cloneRuntimeSnapshot(current)
		case request := <-c.synchronization:
			synchronizationQueue = append(synchronizationQueue, request)
		case result := <-c.synchronizationComplete:
			synchronizationInFlight = false
			diagnostic := SynchronizationDiagnostic{Operation: "refresh_synchronization"}
			if result.err != nil {
				if health.LastKnown == nil {
					health.Classification = SynchronizationUnavailable
				} else {
					health.Classification = SynchronizationStale
				}
				diagnostic.Classification = health.Classification
				diagnostic.Error = result.err.Error()
				if health.LastKnown != nil {
					diagnostic.Observation = health.LastKnown.ObservedAt
				}
				recordSynchronizationDiagnostic(runtime.Diagnostics, diagnostic)
				result.request.result <- fmt.Errorf("refresh synchronization health: %w", result.err)
				continue
			}
			if err := validateSynchronizationObservation(result.observation); err != nil {
				health.Classification = SynchronizationUnavailable
				if health.LastKnown != nil {
					health.Classification = SynchronizationStale
					diagnostic.Observation = health.LastKnown.ObservedAt
				}
				diagnostic.Classification, diagnostic.Error = health.Classification, err.Error()
				recordSynchronizationDiagnostic(runtime.Diagnostics, diagnostic)
				result.request.result <- fmt.Errorf("refresh synchronization health: %w", err)
				continue
			}
			observation := result.observation
			health = SynchronizationHealth{Classification: observation.Classification, ObservedAt: observation.ObservedAt, LastKnown: &observation}
			diagnostic.Classification, diagnostic.Observation = observation.Classification, observation.ObservedAt
			recordSynchronizationDiagnostic(runtime.Diagnostics, diagnostic)
			result.request.result <- nil
		case request := <-c.timeline:
			if err := request.ctx.Err(); err != nil {
				request.result <- fmt.Errorf("replace timeline: %w", err)
				continue
			}
			if err := request.timeline.Validate(); err != nil {
				request.result <- fmt.Errorf("replace timeline: %w", err)
				continue
			}
			timeline = request.timeline
			current = clearedRuntimeSnapshot(adopted, timeline.Mode(), ownedSun)
			request.result <- nil
		case request := <-c.tick:
			if err := request.ctx.Err(); err != nil {
				request.result <- fmt.Errorf("tick timeline: %w", err)
				continue
			}
			if runtime.Clock == nil || runtime.Renderer == nil || runtime.Frames == nil {
				request.result <- fmt.Errorf("tick timeline: %w", ErrRuntimeUnavailable)
				continue
			}
			sample := runtime.Clock.Sample()
			effective, position, err := timeline.EvaluateSample(sample, adopted.Calibration(), adopted.Location())
			if err != nil {
				request.result <- fmt.Errorf("evaluate timeline tick: %w", err)
				continue
			}
			if position == current.Position {
				current.Effective = effective
				request.result <- nil
				continue
			}
			sun := ownedSun
			sun.Position = position
			frame, err := runtime.Renderer.Render(sun)
			if err != nil {
				request.result <- fmt.Errorf("render timeline tick: %w", err)
				continue
			}
			if err := request.ctx.Err(); err != nil {
				request.result <- fmt.Errorf("submit timeline tick: %w", err)
				continue
			}
			if err := runtime.Frames.Submit(frame); err != nil {
				request.result <- fmt.Errorf("submit timeline tick: %w", err)
				continue
			}
			current.Effective, current.Position, current.Sun, current.Frame = effective, position, sun, frame
			request.result <- nil
		case result := <-c.complete:
			if inFlight == nil || result.revision != inFlight.candidate.Revision() {
				continue
			}
			if result.err != nil {
				inFlight.request.result <- fmt.Errorf("persist device state revision %d: %w", result.revision, result.err)
			} else {
				adopted = inFlight.candidate
				current = clearedRuntimeSnapshot(adopted, timeline.Mode(), ownedSun)
				inFlight.request.result <- nil
			}
			inFlight = nil
		}
	}
}

func validateSynchronizationObservation(observation SynchronizationObservation) error {
	if observation.ObservedAt.IsZero() {
		return errors.New("invalid synchronization observation: zero observation time")
	}
	if observation.Classification != SynchronizationSynchronized && observation.Classification != SynchronizationUnsynchronized && observation.Classification != SynchronizationUnknown {
		return fmt.Errorf("invalid synchronization classification %q", observation.Classification)
	}
	return nil
}

func recordSynchronizationDiagnostic(recorder SynchronizationDiagnostics, diagnostic SynchronizationDiagnostic) {
	if isNilInterface(recorder) {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		recorder.RecordSynchronization(diagnostic)
	}()
}

func isNilStore(store DeviceStateStore) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
