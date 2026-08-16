package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cwhite616/sundial/internal/render"
)

type fakeOutput struct {
	mu        sync.Mutex
	writes    []render.Frame
	failures  []error
	started   chan struct{}
	completed chan struct{}
	gate      <-chan struct{}
	clearGate <-chan struct{}
	clearErr  error
	closeErr  error
	cleared   bool
	closed    bool
}

func (f *fakeOutput) WriteFrame(ctx context.Context, frame render.Frame) error {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.gate:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		return err
	}
	f.writes = append(f.writes, render.NewFrame(frame.Pixels()))
	if f.completed != nil {
		select {
		case f.completed <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *fakeOutput) Clear(ctx context.Context) error {
	if f.clearGate != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.clearGate:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = true
	return f.clearErr
}
func (f *fakeOutput) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}
func (f *fakeOutput) values() []uint8 {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := make([]uint8, len(f.writes))
	for i, frame := range f.writes {
		p, _ := frame.Pixel(0)
		values[i] = p.R
	}
	return values
}

func frame(value uint8) render.Frame { return render.NewFrame([]render.Pixel{{R: value}}) }
func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for signal")
		var zero T
		return zero
	}
}

func TestNewWorkerRejectsNilOutputAndCanceledParent(t *testing.T) {
	if _, err := NewWorker(context.Background(), nil, WorkerOptions{}); !errors.Is(err, ErrNilOutput) {
		t.Fatalf("nil output error = %v", err)
	}
	var typedNil *fakeOutput
	if _, err := NewWorker(context.Background(), typedNil, WorkerOptions{}); !errors.Is(err, ErrNilOutput) {
		t.Fatalf("typed nil output error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewWorker(ctx, &fakeOutput{}, WorkerOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parent error = %v", err)
	}
}

func TestWorkerKeepsOnlyNewestPendingFrame(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	completed := make(chan struct{}, 2)
	output := &fakeOutput{gate: gate, started: started, completed: completed}
	w, err := NewWorker(context.Background(), output, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Submit(frame(1)); err != nil {
		t.Fatal(err)
	}
	receive(t, started)
	if err := w.Submit(frame(2)); err != nil {
		t.Fatal(err)
	}
	if err := w.Submit(frame(3)); err != nil {
		t.Fatal(err)
	}
	close(gate)
	receive(t, completed)
	receive(t, completed)
	if values := output.values(); len(values) != 2 || values[0] != 1 || values[1] != 3 {
		t.Fatalf("writes = %v", values)
	}
	w.Close()
}

func TestWorkerRetriesNewestFrameAndSurfacesWrappedError(t *testing.T) {
	injected := errors.New("driver failed")
	backoffStarted := make(chan struct{})
	continueBackoff := make(chan struct{})
	output := &fakeOutput{failures: []error{injected}}
	w, err := NewWorker(context.Background(), output, WorkerOptions{MaxRetries: 1, Backoff: func(ctx context.Context, _ int) error {
		close(backoffStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-continueBackoff:
			return nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Submit(frame(1)); err != nil {
		t.Fatal(err)
	}
	receive(t, backoffStarted)
	if err := w.Submit(frame(9)); err != nil {
		t.Fatal(err)
	}
	close(continueBackoff)
	workerErr := receive(t, w.Errors())
	if !errors.Is(workerErr, injected) {
		t.Fatalf("worker error = %v", workerErr)
	}
	delivered := receive(t, w.Delivered())
	p, _ := delivered.Pixel(0)
	if p.R != 9 {
		t.Fatalf("retried value = %d", p.R)
	}
	w.Close()
}

func TestWorkerCancellationRejectsSubmissionAndQueuedDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	output := &fakeOutput{gate: gate, started: started}
	w, err := NewWorker(ctx, output, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Submit(frame(1)); err != nil {
		t.Fatal(err)
	}
	receive(t, started)
	if err := w.Submit(frame(2)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := w.Submit(frame(3)); !errors.Is(err, ErrWorkerClosed) {
		t.Fatalf("submit after cancel = %v", err)
	}
	receive(t, w.Done())
	w.Close()
	if values := output.values(); len(values) != 0 {
		t.Fatalf("writes after cancellation = %v", values)
	}
}

func TestConcurrentSubmitAndCloseIsLinearized(t *testing.T) {
	output := &fakeOutput{}
	w, err := NewWorker(context.Background(), output, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	result := make(chan error, 1)
	closed := make(chan struct{})
	go func() { <-start; result <- w.Submit(frame(1)) }()
	go func() { <-start; w.Close(); close(closed) }()
	close(start)
	receive(t, closed)
	submitErr := receive(t, result)
	if submitErr != nil && !errors.Is(submitErr, ErrWorkerClosed) {
		t.Fatalf("submit error = %v", submitErr)
	}
	if err := w.Submit(frame(2)); !errors.Is(err, ErrWorkerClosed) {
		t.Fatalf("submit after close = %v", err)
	}
}

func TestWorkerBoundsBlockedClearAndReportsCleanupErrors(t *testing.T) {
	clearGate := make(chan struct{})
	closeErr := errors.New("close failed")
	output := &fakeOutput{clearGate: clearGate, closeErr: closeErr}
	w, err := NewWorker(context.Background(), output, WorkerOptions{CleanupTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	var gotTimeout, gotClose bool
	for err := range w.Errors() {
		gotTimeout = gotTimeout || errors.Is(err, context.DeadlineExceeded)
		gotClose = gotClose || errors.Is(err, closeErr)
	}
	if !gotTimeout || !gotClose {
		t.Fatalf("cleanup errors: timeout=%v close=%v", gotTimeout, gotClose)
	}
	output.mu.Lock()
	closed := output.closed
	output.mu.Unlock()
	if !closed {
		t.Fatal("Close was not attempted")
	}
}

func TestWorkerReportsClearError(t *testing.T) {
	clearErr := errors.New("clear failed")
	output := &fakeOutput{clearErr: clearErr}
	w, err := NewWorker(context.Background(), output, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if workerErr := receive(t, w.Errors()); !errors.Is(workerErr, clearErr) {
		t.Fatalf("clear error = %v", workerErr)
	}
}

func TestWorkerRetryIsBounded(t *testing.T) {
	injected := errors.New("always fails")
	output := &fakeOutput{failures: []error{injected, injected, injected}}
	w, err := NewWorker(context.Background(), output, WorkerOptions{MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Submit(frame(1)); err != nil {
		t.Fatal(err)
	}
	first, second := receive(t, w.Errors()), receive(t, w.Errors())
	var terminal *DeliveryError
	if !errors.Is(first, injected) || !errors.As(second, &terminal) || !terminal.Terminal {
		t.Fatalf("errors = %v, %v", first, second)
	}
	output.mu.Lock()
	remaining := len(output.failures)
	output.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("remaining failures = %d", remaining)
	}
	w.Close()
}
