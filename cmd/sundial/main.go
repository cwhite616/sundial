package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/render"
)

const (
	stripLength      = 144
	driverBrightness = 96
)

var previewSafety = render.Safety{
	RedCurrent: 20, GreenCurrent: 20, BlueCurrent: 20, WhiteCurrent: 20,
	MaxStripCurrent: 127_500, BrightnessCeiling: driverBrightness,
}

func startPreview(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff) (*app.Worker, error) {
	return startPreviewWithHold(ctx, output, safety, backoff, func(context.Context) error { return nil })
}

func startPreviewWithHold(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff, hold func(context.Context) error) (*app.Worker, error) {
	renderer, err := render.New(stripLength, safety)
	if err != nil {
		return nil, fmt.Errorf("initialize preview renderer: %w", err)
	}
	worker, err := app.NewWorker(ctx, output, app.WorkerOptions{MaxRetries: 2, Backoff: backoff})
	if err != nil {
		return nil, fmt.Errorf("initialize preview worker: %w", err)
	}
	for _, sun := range verificationSequence() {
		frame, err := renderer.Render(sun)
		if err != nil {
			_ = worker.Close()
			return nil, fmt.Errorf("render preview: %w", err)
		}
		if err := worker.Submit(frame); err != nil {
			_ = worker.Close()
			return nil, fmt.Errorf("submit preview: %w", err)
		}
		if err := waitForDelivery(ctx, worker, frame); err != nil {
			_ = worker.Close()
			return nil, err
		}
		if sun != finalPreviewSun() {
			if err := hold(ctx); err != nil {
				_ = worker.Close()
				return nil, fmt.Errorf("hold diagnostic preview: %w", err)
			}
		}
	}
	return worker, nil
}

func waitForDelivery(ctx context.Context, worker *app.Worker, frame render.Frame) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for preview delivery: %w", ctx.Err())
		case delivered, ok := <-worker.Delivered():
			if !ok {
				return errors.New("preview worker stopped before delivery")
			}
			if !framesEqual(delivered, frame) {
				return errors.New("preview delivered an unexpected frame")
			}
			return nil
		case workerErr, ok := <-worker.Errors():
			if !ok {
				return errors.New("preview worker stopped before delivery")
			}
			var deliveryErr *app.DeliveryError
			if errors.As(workerErr, &deliveryErr) && deliveryErr.Terminal {
				return fmt.Errorf("deliver preview: %w", workerErr)
			}
		}
	}
}

func verificationSequence() []render.ArtificialSun {
	position := stripLength / 2
	return []render.ArtificialSun{
		{Position: position, Color: render.Pixel{R: 255}},
		{Position: position, Color: render.Pixel{G: 255}},
		{Position: position, Color: render.Pixel{B: 255}},
		{Position: position, Color: render.Pixel{W: 255}},
		finalPreviewSun(),
	}
}

func finalPreviewSun() render.ArtificialSun {
	// Provisional Red Sun palette value 90A03500 in WWRRGGBB form.
	return render.ArtificialSun{Position: stripLength / 2, Color: render.Pixel{W: 0x90, R: 0xA0, G: 0x35}}
}

func holdPhysicalDiagnostic(ctx context.Context) error {
	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func framesEqual(left, right render.Frame) bool {
	if left.Len() != right.Len() {
		return false
	}
	leftPixels, rightPixels := left.Pixels(), right.Pixels()
	for i := range leftPixels {
		if leftPixels[i] != rightPixels[i] {
			return false
		}
	}
	return true
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	driver, err := newPreviewOutput(ctx)
	if err != nil {
		return fmt.Errorf("initialize preview output: %w", err)
	}
	worker, err := startPreviewWithHold(ctx, driver, previewSafety, func(ctx context.Context, attempt int) error {
		timer := time.NewTimer(time.Duration(attempt) * 25 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}, holdPhysicalDiagnostic)
	if err != nil {
		return err
	}
	fmt.Printf("verified bounded R/G/B/W channels and previewed artificial sun at pixel %d of %d; press Ctrl-C to stop\n", stripLength/2, stripLength)
	<-ctx.Done()
	if err := worker.Close(); err != nil {
		return fmt.Errorf("shut down preview output: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Printf("sundial preview failed: %v", err)
		os.Exit(1)
	}
}
