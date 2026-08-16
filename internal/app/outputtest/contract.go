// Package outputtest provides reusable contract coverage for Output adapters.
package outputtest

import (
	"context"
	"errors"
	"testing"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/render"
)

type Harness struct {
	New          func(int) (app.Output, error)
	Snapshot     func(app.Output) render.Frame
	InjectErrors func(app.Output, ...error)
}

func Run(t *testing.T, harness Harness) {
	t.Helper()
	output, err := harness.New(2)
	if err != nil {
		t.Fatal(err)
	}
	input := []render.Pixel{{R: 1, G: 2, B: 3, W: 4}, {W: 5}}
	if err := output.WriteFrame(context.Background(), render.NewFrame(input)); err != nil {
		t.Fatal(err)
	}
	input[0] = render.Pixel{}
	snapshot := harness.Snapshot(output)
	pixel, _ := snapshot.Pixel(0)
	if pixel != (render.Pixel{R: 1, G: 2, B: 3, W: 4}) {
		t.Fatalf("RGBW snapshot = %+v", pixel)
	}
	pixels := snapshot.Pixels()
	pixels[0] = render.Pixel{}
	pixel, _ = harness.Snapshot(output).Pixel(0)
	if pixel.R != 1 {
		t.Fatal("snapshot storage was mutable")
	}
	if err := output.WriteFrame(context.Background(), render.NewFrame(make([]render.Pixel, 1))); err == nil {
		t.Fatal("expected exact-length rejection")
	}
	injected := errors.New("injected")
	harness.InjectErrors(output, injected)
	if err := output.WriteFrame(context.Background(), render.NewFrame(make([]render.Pixel, 2))); !errors.Is(err, injected) {
		t.Fatalf("injected error = %v", err)
	}
	if err := output.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, pixel := range harness.Snapshot(output).Pixels() {
		if pixel != (render.Pixel{}) {
			t.Fatalf("clear pixel %d = %+v", i, pixel)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := output.WriteFrame(canceled, render.NewFrame(make([]render.Pixel, 2))); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write = %v", err)
	}
	if err := output.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := output.WriteFrame(context.Background(), render.NewFrame(make([]render.Pixel, 2))); err == nil {
		t.Fatal("expected write-after-close rejection")
	}
}
