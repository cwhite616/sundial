package simulated

import (
	"context"
	"errors"
	"testing"

	"github.com/cwhite616/sundial/internal/render"
)

func TestDriverContract(t *testing.T) {
	d, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	input := []render.Pixel{{R: 1, G: 2, B: 3, W: 4}, {W: 5}}
	frame := render.NewFrame(input)
	if err := d.WriteFrame(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	input[0] = render.Pixel{}
	snapshot := d.Snapshot()
	p, _ := snapshot.Pixel(0)
	if p != (render.Pixel{R: 1, G: 2, B: 3, W: 4}) {
		t.Fatalf("RGBW snapshot = %+v", p)
	}
	copy := snapshot.Pixels()
	copy[0] = render.Pixel{}
	p, _ = d.Snapshot().Pixel(0)
	if p.R != 1 {
		t.Fatal("snapshot storage was mutable")
	}
	if err := d.WriteFrame(context.Background(), render.NewFrame(make([]render.Pixel, 1))); err == nil {
		t.Fatal("expected length rejection")
	}
	if err := d.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := d.Snapshot().Pixels(); got[0] != (render.Pixel{}) || got[1] != (render.Pixel{}) {
		t.Fatalf("clear = %+v", got)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if !d.Closed() {
		t.Fatal("driver not closed")
	}
	if err := d.WriteFrame(context.Background(), frame); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed write error = %v", err)
	}
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
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if pixel, _ := d.Snapshot().Pixel(0); pixel != (render.Pixel{}) {
		t.Fatalf("closed snapshot = %+v", pixel)
	}
}
