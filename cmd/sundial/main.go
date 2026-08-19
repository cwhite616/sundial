package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/clock"
	"github.com/cwhite616/sundial/internal/render"
)

const (
	stripLength               = 144
	nativeDriverBrightness    = 255
	rendererBrightnessCeiling = 96
)

var previewSafety = render.Safety{
	RedCurrent: 20, GreenCurrent: 20, BlueCurrent: 20, WhiteCurrent: 20,
	MaxStripCurrent: 127_500, BrightnessCeiling: rendererBrightnessCeiling,
}

type tuningTrial struct {
	ID      string
	Instant time.Time
	Color   render.Pixel
}

type tuningResult struct {
	ID              string
	Effective       time.Time
	LocalTime       clock.TimeOfDay
	Position        int
	IntervalFrom    clock.CalibrationPoint
	IntervalTo      clock.CalibrationPoint
	CrossesMidnight bool
	RequestedColor  render.Pixel
	Frame           render.Frame
}

type tuningDiagnostic func(tuningResult) error

// startTuningSequence evaluates a finite, caller-supplied set of attributable
// absolute instants. The caller retains responsibility for using an accepted
// physical setup revision and recording human observations before tuning.
func startTuningSequence(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff, calibration clock.Calibration, location *time.Location, trials []tuningTrial, diagnostic tuningDiagnostic) (*app.Worker, error) {
	renderer, err := render.New(stripLength, safety)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("initialize tuning renderer: %w", err), closeUnownedOutput(output))
	}
	worker, err := app.NewWorker(ctx, output, app.WorkerOptions{MaxRetries: 2, Backoff: backoff})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("initialize tuning worker: %w", err), closeUnownedOutput(output))
	}
	for index, trial := range trials {
		if err := ctx.Err(); err != nil {
			return nil, failTuning(worker, fmt.Errorf("trial %d %q canceled: %w", index, trial.ID, err))
		}
		if trial.ID == "" {
			return nil, failTuning(worker, fmt.Errorf("trial %d: empty identifier", index))
		}
		effective, position, err := clock.NewFixedTimeline(trial.Instant).Evaluate(time.Time{}, calibration, location)
		if err != nil {
			return nil, failTuning(worker, fmt.Errorf("evaluate trial %q: %w", trial.ID, err))
		}
		frame, err := renderer.Render(render.ArtificialSun{Position: position, Color: trial.Color})
		if err != nil {
			return nil, failTuning(worker, fmt.Errorf("render trial %q: %w", trial.ID, err))
		}
		if err := worker.Submit(frame); err != nil {
			return nil, failTuning(worker, fmt.Errorf("submit trial %q: %w", trial.ID, err))
		}
		if err := waitForDelivery(ctx, worker, frame); err != nil {
			return nil, failTuning(worker, fmt.Errorf("trial %q: %w", trial.ID, err))
		}
		if diagnostic != nil {
			localTime := clock.TimeOfDayFromTime(effective.In(location))
			from, to, crossesMidnight := tuningInterval(calibration, localTime)
			result := tuningResult{ID: trial.ID, Effective: effective, LocalTime: localTime, Position: position, IntervalFrom: from, IntervalTo: to, CrossesMidnight: crossesMidnight, RequestedColor: trial.Color, Frame: frame}
			if err := diagnostic(result); err != nil {
				return nil, failTuning(worker, fmt.Errorf("record trial %q diagnostic: %w", trial.ID, err))
			}
		}
	}
	return worker, nil
}

func tuningInterval(calibration clock.Calibration, value clock.TimeOfDay) (clock.CalibrationPoint, clock.CalibrationPoint, bool) {
	for _, point := range calibration.Points() {
		if value == point.Time {
			return point, point, false
		}
	}
	for _, pair := range calibration.CyclicPairs() {
		if !pair.CrossesMidnight && value.Duration() > pair.From.Time.Duration() && value.Duration() < pair.To.Time.Duration() {
			return pair.From, pair.To, false
		}
		if pair.CrossesMidnight && (value.Duration() > pair.From.Time.Duration() || value.Duration() < pair.To.Time.Duration()) {
			return pair.From, pair.To, true
		}
	}
	return clock.CalibrationPoint{}, clock.CalibrationPoint{}, false
}

func failTuning(worker *app.Worker, cause error) error {
	return errors.Join(cause, worker.Close())
}

func startPreview(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff) (*app.Worker, error) {
	return startPreviewWithHold(ctx, output, safety, backoff, func(context.Context) error { return nil })
}

func startPreviewWithHold(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff, hold func(context.Context) error) (*app.Worker, error) {
	renderer, err := render.New(stripLength, safety)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("initialize preview renderer: %w", err), closeUnownedOutput(output))
	}
	worker, err := app.NewWorker(ctx, output, app.WorkerOptions{MaxRetries: 2, Backoff: backoff})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("initialize preview worker: %w", err), closeUnownedOutput(output))
	}
	for _, step := range verificationSequence() {
		frame, err := renderer.Render(step.sun)
		if err != nil {
			return nil, failPreview(worker, fmt.Errorf("render preview: %w", err))
		}
		if err := worker.Submit(frame); err != nil {
			return nil, failPreview(worker, fmt.Errorf("submit preview: %w", err))
		}
		if err := waitForDelivery(ctx, worker, frame); err != nil {
			return nil, failPreview(worker, err)
		}
		if step.hold {
			if err := hold(ctx); err != nil {
				return nil, failPreview(worker, fmt.Errorf("hold diagnostic preview: %w", err))
			}
		}
	}
	return worker, nil
}

func closeUnownedOutput(output app.Output) error {
	if output == nil {
		return nil
	}
	value := reflect.ValueOf(output)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := output.Close(ctx); err != nil {
		return fmt.Errorf("close preview output after startup failure: %w", err)
	}
	return nil
}

func failPreview(worker *app.Worker, cause error) error {
	return errors.Join(cause, worker.Close())
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

type verificationStep struct {
	sun  render.ArtificialSun
	hold bool
}

func verificationSequence() []verificationStep {
	position := stripLength / 2
	return []verificationStep{
		{sun: render.ArtificialSun{Position: position, Color: render.Pixel{R: 255}}, hold: true},
		{sun: render.ArtificialSun{Position: position, Color: render.Pixel{G: 255}}, hold: true},
		{sun: render.ArtificialSun{Position: position, Color: render.Pixel{B: 255}}, hold: true},
		{sun: render.ArtificialSun{Position: position, Color: render.Pixel{W: 255}}, hold: true},
		{sun: finalPreviewSun()},
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

func selectedDiagnosticHold() func(context.Context) error {
	if physicalPreviewOutput {
		return holdPhysicalDiagnostic
	}
	return func(context.Context) error { return nil }
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
	fmt.Printf("output mode: %s\n", previewOutputDescription)
	diagnosticHold := selectedDiagnosticHold()
	worker, err := startPreviewWithHold(ctx, driver, previewSafety, func(ctx context.Context, attempt int) error {
		timer := time.NewTimer(time.Duration(attempt) * 25 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}, diagnosticHold)
	if err != nil {
		return err
	}
	if physicalPreviewOutput {
		fmt.Printf("emitted bounded physical R/G/B/W diagnostic sequence and previewed artificial sun at pixel %d of %d; press Ctrl-C to stop\n", stripLength/2, stripLength)
	} else {
		fmt.Printf("completed simulated R/G/B/W sequence at pixel %d of %d; no physical verification was performed; press Ctrl-C to stop\n", stripLength/2, stripLength)
	}
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
