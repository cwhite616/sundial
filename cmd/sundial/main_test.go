package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/render"
)

type previewOutput struct {
	mu       sync.Mutex
	failures []error
	writes   []render.Frame
}

func (o *previewOutput) WriteFrame(_ context.Context, frame render.Frame) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.failures) > 0 {
		err := o.failures[0]
		o.failures = o.failures[1:]
		return err
	}
	o.writes = append(o.writes, render.NewFrame(frame.Pixels()))
	return nil
}
func (*previewOutput) Clear(context.Context) error { return nil }
func (*previewOutput) Close(context.Context) error { return nil }

func TestStartPreviewRetriesBeforeReportingDeliveredSuccess(t *testing.T) {
	injected := errors.New("transient")
	output := &previewOutput{failures: []error{injected}}
	worker, err := startPreview(context.Background(), output, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10000, BrightnessCeiling: 255}, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	output.mu.Lock()
	writes := append([]render.Frame(nil), output.writes...)
	output.mu.Unlock()
	if len(writes) != 1 {
		t.Fatalf("successful writes = %d", len(writes))
	}
	pixel, _ := writes[0].Pixel(stripLength / 2)
	if pixel != (render.Pixel{R: 255, G: 120, W: 180}) {
		t.Fatalf("center pixel = %+v", pixel)
	}
}

func TestStartPreviewReturnsStartupAndTerminalFailures(t *testing.T) {
	if _, err := startPreview(context.Background(), nil, previewSafety, nil); !errors.Is(err, app.ErrNilOutput) {
		t.Fatalf("nil output error = %v", err)
	}
	invalid := previewSafety
	invalid.MaxStripCurrent = 0
	if _, err := startPreview(context.Background(), &previewOutput{}, invalid, nil); !errors.Is(err, render.ErrInvalidSafety) {
		t.Fatalf("render startup error = %v", err)
	}
	injected := errors.New("terminal")
	output := &previewOutput{failures: []error{injected, injected, injected}}
	if _, err := startPreview(context.Background(), output, previewSafety, func(context.Context, int) error { return nil }); !errors.Is(err, injected) {
		t.Fatalf("terminal error = %v", err)
	}
}

func TestStartPreviewReturnsTerminalBackoffFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	backoffErr := errors.New("backoff failed")
	output := &previewOutput{failures: []error{writeErr}}
	_, err := startPreview(context.Background(), output, previewSafety, func(context.Context, int) error {
		return backoffErr
	})
	if !errors.Is(err, writeErr) || !errors.Is(err, backoffErr) {
		t.Fatalf("startup error = %v", err)
	}
}
