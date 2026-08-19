package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/cwhite616/sundial/internal/clock"
)

var (
	ErrNilStore         = errors.New("device state store is nil")
	ErrControllerClosed = errors.New("device state controller is closed")
)

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

type persistenceResult struct {
	revision uint64
	err      error
}

type pendingReplacement struct {
	request   replacementRequest
	candidate DeviceState
}

type Controller struct {
	ctx       context.Context
	cancel    context.CancelFunc
	replace   chan replacementRequest
	snapshot  chan snapshotRequest
	complete  chan persistenceResult
	done      chan struct{}
	closeOnce sync.Once
}

func NewController(parent context.Context, initial DeviceState, store DeviceStateStore) (*Controller, error) {
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
	c := &Controller{
		ctx:      ctx,
		cancel:   cancel,
		replace:  make(chan replacementRequest),
		snapshot: make(chan snapshotRequest),
		complete: make(chan persistenceResult, 1),
		done:     make(chan struct{}),
	}
	go c.run(initial, store)
	return c, nil
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

func (c *Controller) Close() {
	c.closeOnce.Do(c.cancel)
	<-c.done
}

func (c *Controller) run(adopted DeviceState, store DeviceStateStore) {
	defer close(c.done)
	var queue []replacementRequest
	var inFlight *pendingReplacement

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

	for {
		startNext()
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
		case result := <-c.complete:
			if inFlight == nil || result.revision != inFlight.candidate.Revision() {
				continue
			}
			if result.err != nil {
				inFlight.request.result <- fmt.Errorf("persist device state revision %d: %w", result.revision, result.err)
			} else {
				adopted = inFlight.candidate
				inFlight.request.result <- nil
			}
			inFlight = nil
		}
	}
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
