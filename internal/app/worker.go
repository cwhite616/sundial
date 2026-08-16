package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/cwhite616/sundial/internal/render"
)

var (
	ErrWorkerClosed = errors.New("output worker is closed")
	ErrNilOutput    = errors.New("output is nil")
)

type Backoff func(context.Context, int) error

type WorkerOptions struct {
	MaxRetries     int
	Backoff        Backoff
	CleanupTimeout time.Duration
}

type DeliveryError struct {
	Attempt  int
	Terminal bool
	Err      error
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("write output frame (attempt %d): %v", e.Attempt, e.Err)
}
func (e *DeliveryError) Unwrap() error { return e.Err }

type Worker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	inbox     chan render.Frame
	errors    chan error
	delivered chan render.Frame
	done      chan struct{}
	mu        sync.Mutex
	closed    bool
	closeErr  error
	once      sync.Once
}

func NewWorker(parent context.Context, output Output, options WorkerOptions) (*Worker, error) {
	if output == nil || (reflect.ValueOf(output).Kind() == reflect.Pointer && reflect.ValueOf(output).IsNil()) {
		return nil, ErrNilOutput
	}
	if err := parent.Err(); err != nil {
		return nil, fmt.Errorf("initialize output worker: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	w := &Worker{ctx: ctx, cancel: cancel, inbox: make(chan render.Frame, 1), errors: make(chan error, 16), delivered: make(chan render.Frame, 1), done: make(chan struct{})}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	if options.Backoff == nil {
		options.Backoff = func(context.Context, int) error { return nil }
	}
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = time.Second
	}
	go w.run(output, options)
	return w, nil
}

// Submit atomically replaces any pending frame and never waits for driver I/O.
func (w *Worker) Submit(frame render.Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.ctx.Err() != nil {
		return ErrWorkerClosed
	}
	select {
	case <-w.inbox:
	default:
	}
	w.inbox <- frame
	if w.ctx.Err() != nil {
		select {
		case <-w.inbox:
		default:
		}
		return ErrWorkerClosed
	}
	return nil
}

func (w *Worker) Errors() <-chan error           { return w.errors }
func (w *Worker) Delivered() <-chan render.Frame { return w.delivered }
func (w *Worker) Done() <-chan struct{}          { return w.done }

func (w *Worker) Close() error {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		select {
		case <-w.inbox:
		default:
		}
		w.cancel()
		w.mu.Unlock()
	})
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeErr
}

func (w *Worker) report(err error) {
	select {
	case w.errors <- err:
	default:
	}
}

// reportTerminal retains lifecycle-ending errors even when nonterminal
// notifications have filled the bounded error channel.
func (w *Worker) reportTerminal(err error) {
	for {
		select {
		case w.errors <- err:
			return
		default:
		}
		select {
		case <-w.errors:
		default:
		}
	}
}

func (w *Worker) run(output Output, options WorkerOptions) {
	defer close(w.done)
	defer close(w.delivered)
	defer close(w.errors)
	defer func() {
		err := w.cleanup(output, options.CleanupTimeout)
		w.mu.Lock()
		w.closeErr = err
		w.mu.Unlock()
	}()
	for {
		select {
		case <-w.ctx.Done():
			return
		case frame := <-w.inbox:
			if w.ctx.Err() != nil {
				return
			}
			w.deliver(output, options, frame)
		}
	}
}

func (w *Worker) cleanup(output Output, timeout time.Duration) error {
	var cleanupErrors []error
	clearCtx, cancelClear := context.WithTimeout(context.Background(), timeout)
	if err := output.Clear(clearCtx); err != nil {
		wrapped := fmt.Errorf("clear output during shutdown: %w", err)
		cleanupErrors = append(cleanupErrors, wrapped)
		w.reportTerminal(wrapped)
	}
	cancelClear()

	closeCtx, cancelClose := context.WithTimeout(context.Background(), timeout)
	if err := output.Close(closeCtx); err != nil {
		wrapped := fmt.Errorf("close output during shutdown: %w", err)
		cleanupErrors = append(cleanupErrors, wrapped)
		w.reportTerminal(wrapped)
	}
	cancelClose()
	return errors.Join(cleanupErrors...)
}

func (w *Worker) deliveredFrame(frame render.Frame) {
	select {
	case <-w.delivered:
	default:
	}
	select {
	case w.delivered <- frame:
	default:
	}
}

func (w *Worker) deliver(output Output, options WorkerOptions, frame render.Frame) {
	for attempt := 0; ; attempt++ {
		if w.ctx.Err() != nil {
			return
		}
		err := output.WriteFrame(w.ctx, frame)
		if err == nil {
			if w.ctx.Err() == nil {
				w.deliveredFrame(frame)
			}
			return
		}
		terminal := attempt >= options.MaxRetries
		deliveryErr := &DeliveryError{Attempt: attempt + 1, Terminal: terminal, Err: err}
		if terminal {
			w.reportTerminal(deliveryErr)
		} else {
			w.report(deliveryErr)
		}
		if terminal {
			return
		}
		if backoffErr := options.Backoff(w.ctx, attempt+1); backoffErr != nil {
			if !errors.Is(backoffErr, context.Canceled) {
				w.reportTerminal(&DeliveryError{
					Attempt:  attempt + 1,
					Terminal: true,
					Err: errors.Join(
						err,
						fmt.Errorf("wait to retry output frame: %w", backoffErr),
					),
				})
			}
			return
		}
		if w.ctx.Err() != nil {
			return
		}
		select {
		case newest := <-w.inbox:
			frame = newest
		default:
		}
	}
}
