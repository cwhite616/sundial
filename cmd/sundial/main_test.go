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
	clearErr error
	closeErr error
	clears   int
	closes   int
}

func TestPhysicalConfigurationUsesSafeExactLength(t *testing.T) {
	if stripLength != 144 {
		t.Fatalf("strip length = %d", stripLength)
	}
	if previewSafety.MaxStripCurrent != 127_500 || previewSafety.BrightnessCeiling != rendererBrightnessCeiling {
		t.Fatalf("physical safety = %+v", previewSafety)
	}
	if nativeDriverBrightness != 255 || rendererBrightnessCeiling != 96 {
		t.Fatalf("brightness controls: native=%d renderer=%d", nativeDriverBrightness, rendererBrightnessCeiling)
	}
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
func (o *previewOutput) Clear(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clears++
	return o.clearErr
}
func (o *previewOutput) Close(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closes++
	return o.closeErr
}

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
	if len(writes) != 5 {
		t.Fatalf("successful writes = %d", len(writes))
	}
	want := []render.Pixel{{R: 255}, {G: 255}, {B: 255}, {W: 255}, {W: 0x90, R: 0xA0, G: 0x35}}
	for i, frame := range writes {
		pixel, _ := frame.Pixel(stripLength / 2)
		if pixel != want[i] {
			t.Fatalf("frame %d center pixel = %+v, want %+v", i, pixel, want[i])
		}
	}
}

func TestVerificationHoldsExactlyFourDiagnostics(t *testing.T) {
	var holds int
	worker, err := startPreviewWithHold(context.Background(), &previewOutput{}, previewSafety, nil, func(context.Context) error {
		holds++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if holds != 4 {
		t.Fatalf("holds = %d, want 4", holds)
	}
	steps := verificationSequence()
	if len(steps) != 5 || steps[4].hold {
		t.Fatalf("verification hold flags = %+v", steps)
	}
}

func TestHoldFailureAndCancellationAbortAndPreserveCleanupErrors(t *testing.T) {
	holdErr := errors.New("hold failed")
	clearErr := errors.New("clear failed")
	closeErr := errors.New("close failed")
	output := &previewOutput{clearErr: clearErr, closeErr: closeErr}
	_, err := startPreviewWithHold(context.Background(), output, previewSafety, nil, func(context.Context) error { return holdErr })
	if !errors.Is(err, holdErr) || !errors.Is(err, clearErr) || !errors.Is(err, closeErr) {
		t.Fatalf("hold/cleanup error = %v", err)
	}
	output.mu.Lock()
	writes, clears, closes := len(output.writes), output.clears, output.closes
	output.mu.Unlock()
	if writes != 1 || clears != 1 || closes != 1 {
		t.Fatalf("cleanup state: writes=%d clears=%d closes=%d", writes, clears, closes)
	}

	ctx, cancel := context.WithCancel(context.Background())
	output = &previewOutput{}
	_, err = startPreviewWithHold(ctx, output, previewSafety, nil, func(context.Context) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hold error = %v", err)
	}
}

func TestStartupAndDeliveryFailuresPreserveCleanupErrors(t *testing.T) {
	cleanupErr := errors.New("startup close failed")
	invalid := previewSafety
	invalid.MaxStripCurrent = 0
	output := &previewOutput{closeErr: cleanupErr}
	_, err := startPreview(context.Background(), output, invalid, nil)
	if !errors.Is(err, render.ErrInvalidSafety) || !errors.Is(err, cleanupErr) {
		t.Fatalf("renderer/cleanup error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	output = &previewOutput{closeErr: cleanupErr}
	_, err = startPreview(canceled, output, previewSafety, nil)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Fatalf("worker-startup/cleanup error = %v", err)
	}

	writeErr := errors.New("write failed")
	clearErr := errors.New("clear failed")
	output = &previewOutput{failures: []error{writeErr, writeErr, writeErr}, clearErr: clearErr, closeErr: cleanupErr}
	_, err = startPreview(context.Background(), output, previewSafety, nil)
	if !errors.Is(err, writeErr) || !errors.Is(err, clearErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("delivery/cleanup error = %v", err)
	}
}

func TestVerificationSequenceIsCentrallyBounded(t *testing.T) {
	output := &previewOutput{}
	worker, err := startPreview(context.Background(), output, previewSafety, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	output.mu.Lock()
	writes := append([]render.Frame(nil), output.writes...)
	output.mu.Unlock()
	if len(writes) != 5 {
		t.Fatalf("verification frames = %d", len(writes))
	}
	for frameIndex, frame := range writes {
		var estimated uint64
		for pixelIndex, pixel := range frame.Pixels() {
			channels := []uint8{pixel.R, pixel.G, pixel.B, pixel.W}
			for _, channel := range channels {
				if channel > rendererBrightnessCeiling {
					t.Fatalf("frame %d pixel %d channel = %d, ceiling %d", frameIndex, pixelIndex, channel, rendererBrightnessCeiling)
				}
				estimated += uint64(channel) * 20
			}
		}
		if estimated > previewSafety.MaxStripCurrent {
			t.Fatalf("frame %d estimated current = %d, budget %d", frameIndex, estimated, previewSafety.MaxStripCurrent)
		}
	}
	want := []render.Pixel{{R: 96}, {G: 96}, {B: 96}, {W: 96}, {W: 54, R: 60, G: 19}}
	for i, frame := range writes {
		pixel, _ := frame.Pixel(stripLength / 2)
		if pixel != want[i] {
			t.Fatalf("bounded frame %d = %+v, want %+v", i, pixel, want[i])
		}
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
