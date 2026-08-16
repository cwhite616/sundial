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
	"github.com/cwhite616/sundial/internal/leds/simulated"
	"github.com/cwhite616/sundial/internal/render"
)

const stripLength = 60

var previewSafety = render.Safety{
	RedCurrent: 20, GreenCurrent: 20, BlueCurrent: 20, WhiteCurrent: 20,
	MaxStripCurrent: 4000, BrightnessCeiling: 96,
}

func startPreview(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff) (*app.Worker, error) {
	renderer, err := render.New(stripLength, safety)
	if err != nil {
		return nil, fmt.Errorf("initialize preview renderer: %w", err)
	}
	worker, err := app.NewWorker(ctx, output, app.WorkerOptions{MaxRetries: 2, Backoff: backoff})
	if err != nil {
		return nil, fmt.Errorf("initialize preview worker: %w", err)
	}
	frame, err := renderer.Render(render.ArtificialSun{Position: stripLength / 2, Color: render.Pixel{R: 255, G: 120, W: 180}})
	if err != nil {
		worker.Close()
		return nil, fmt.Errorf("render preview: %w", err)
	}
	if err := worker.Submit(frame); err != nil {
		worker.Close()
		return nil, fmt.Errorf("submit preview: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			worker.Close()
			return nil, fmt.Errorf("wait for preview delivery: %w", ctx.Err())
		case delivered, ok := <-worker.Delivered():
			if !ok {
				worker.Close()
				return nil, errors.New("preview worker stopped before delivery")
			}
			if !framesEqual(delivered, frame) {
				worker.Close()
				return nil, errors.New("preview delivered an unexpected frame")
			}
			return worker, nil
		case workerErr, ok := <-worker.Errors():
			if !ok {
				worker.Close()
				return nil, errors.New("preview worker stopped before delivery")
			}
			var deliveryErr *app.DeliveryError
			if errors.As(workerErr, &deliveryErr) && deliveryErr.Terminal {
				worker.Close()
				return nil, fmt.Errorf("deliver preview: %w", workerErr)
			}
		}
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
	driver, err := simulated.New(stripLength)
	if err != nil {
		return fmt.Errorf("initialize preview output: %w", err)
	}
	worker, err := startPreview(ctx, driver, previewSafety, func(ctx context.Context, attempt int) error {
		timer := time.NewTimer(time.Duration(attempt) * 25 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})
	if err != nil {
		return err
	}
	defer worker.Close()
	fmt.Printf("previewed artificial sun at pixel %d of %d; press Ctrl-C to stop\n", stripLength/2, stripLength)
	<-ctx.Done()
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Printf("sundial preview failed: %v", err)
		os.Exit(1)
	}
}
