package simulated

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cwhite616/sundial/internal/render"
)

var ErrClosed = errors.New("simulated LED driver is closed")

// Driver is a pure-Go, observable output adapter.
type Driver struct {
	mu       sync.Mutex
	length   int
	frame    render.Frame
	writes   []render.Frame
	failures []error
	gate     <-chan struct{}
	closed   bool
}

func New(length int) (*Driver, error) {
	if length <= 0 {
		return nil, fmt.Errorf("initialize simulated LED driver: strip length must be positive: %d", length)
	}
	dark, _ := render.DarkFrame(length)
	return &Driver{length: length, frame: dark}, nil
}

func (d *Driver) WriteFrame(ctx context.Context, frame render.Frame) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write simulated frame: %w", err)
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	if frame.Len() != d.length {
		d.mu.Unlock()
		return fmt.Errorf("write simulated frame: length %d does not match strip length %d", frame.Len(), d.length)
	}
	gate := d.gate
	d.mu.Unlock()
	if gate != nil {
		select {
		case <-ctx.Done():
			return fmt.Errorf("write simulated frame: %w", ctx.Err())
		case <-gate:
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if len(d.failures) > 0 {
		err := d.failures[0]
		d.failures = d.failures[1:]
		return fmt.Errorf("write simulated frame: %w", err)
	}
	snapshot := render.NewFrame(frame.Pixels())
	d.frame = snapshot
	d.writes = append(d.writes, snapshot)
	return nil
}

func (d *Driver) Clear(ctx context.Context) error {
	dark, _ := render.DarkFrame(d.length)
	return d.WriteFrame(ctx, dark)
}

func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dark, _ := render.DarkFrame(d.length)
	d.frame = dark
	d.closed = true
	return nil
}

func (d *Driver) Snapshot() render.Frame {
	d.mu.Lock()
	defer d.mu.Unlock()
	return render.NewFrame(d.frame.Pixels())
}

func (d *Driver) Writes() []render.Frame {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]render.Frame, len(d.writes))
	for i, frame := range d.writes {
		result[i] = render.NewFrame(frame.Pixels())
	}
	return result
}

func (d *Driver) InjectErrors(errs ...error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failures = append(d.failures, errs...)
}

func (d *Driver) SetWriteGate(gate <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gate = gate
}

func (d *Driver) Closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}
