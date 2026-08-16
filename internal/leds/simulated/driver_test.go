package simulated

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/app/outputtest"
	"github.com/cwhite616/sundial/internal/render"
)

func TestDriverContract(t *testing.T) {
	outputtest.Run(t, outputtest.Harness{
		New: func(length int) (app.Output, error) { return New(length) },
		Snapshot: func(output app.Output) render.Frame {
			return output.(*Driver).Snapshot()
		},
		InjectErrors: func(output app.Output, errs ...error) {
			output.(*Driver).InjectErrors(errs...)
		},
	})
}

func TestDriverInjectsErrorsAndBlockingDeterministically(t *testing.T) {
	d, _ := New(1)
	injected := errors.New("injected")
	d.InjectErrors(injected)
	if err := d.WriteFrame(context.Background(), render.NewFrame([]render.Pixel{{R: 1}})); !errors.Is(err, injected) {
		t.Fatalf("error = %v", err)
	}
	gate := make(chan struct{})
	d.SetWriteGate(gate)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.WriteFrame(ctx, render.NewFrame([]render.Pixel{{R: 1}})); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestDriverRejectsCanceledContextWithoutGate(t *testing.T) {
	d, _ := New(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.WriteFrame(ctx, render.NewFrame([]render.Pixel{{R: 1}})); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if pixel, _ := d.Snapshot().Pixel(0); pixel != (render.Pixel{}) {
		t.Fatalf("canceled write energized output: %+v", pixel)
	}
}

func TestDirectCloseLeavesDarkSnapshot(t *testing.T) {
	d, _ := New(1)
	if err := d.WriteFrame(context.Background(), render.NewFrame([]render.Pixel{{R: 255, W: 10}})); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pixel, _ := d.Snapshot().Pixel(0); pixel != (render.Pixel{}) {
		t.Fatalf("closed snapshot = %+v", pixel)
	}
}

type cancelAfterFirstCheck struct{ checks atomic.Int32 }

func (c *cancelAfterFirstCheck) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterFirstCheck) Done() <-chan struct{}       { return nil }
func (c *cancelAfterFirstCheck) Value(any) any               { return nil }
func (c *cancelAfterFirstCheck) Err() error {
	if c.checks.Add(1) > 1 {
		return context.Canceled
	}
	return nil
}

func TestDriverRechecksCancellationBeforeCommittingFrame(t *testing.T) {
	d, _ := New(1)
	gate := make(chan struct{})
	close(gate)
	d.SetWriteGate(gate)
	err := d.WriteFrame(&cancelAfterFirstCheck{}, render.NewFrame([]render.Pixel{{R: 255}}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("write error = %v", err)
	}
	if pixel, _ := d.Snapshot().Pixel(0); pixel != (render.Pixel{}) {
		t.Fatalf("canceled write committed %+v", pixel)
	}
}
