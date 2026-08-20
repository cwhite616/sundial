package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/clock"
	"github.com/cwhite616/sundial/internal/config"
	"github.com/cwhite616/sundial/internal/render"
	"github.com/cwhite616/sundial/internal/store"
	"github.com/cwhite616/sundial/internal/timesync"
)

const (
	stripLength               = 144
	nativeDriverBrightness    = 255
	rendererBrightnessCeiling = 255
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

// systemClock is the composition-root implementation of the application's
// System Clock port. Domain packages receive its samples and never read wall
// time themselves.
type systemClock struct{ origin time.Time }

func newSystemClock() systemClock { return systemClock{origin: time.Now()} }

func (c systemClock) Sample() clock.Sample {
	now := time.Now()
	return clock.Sample{Wall: now, Monotonic: now.Sub(c.origin)}
}

func runtimeControllerOptions(renderer *render.Renderer, frames app.FrameSubmitter, sun render.ArtificialSun) app.RuntimeOptions {
	return app.RuntimeOptions{Clock: newSystemClock(), Renderer: renderer, Frames: frames, Sun: sun, Synchronization: timesync.NewTimedatectl(), Diagnostics: synchronizationDiagnosticRecorder{writer: os.Stderr}}
}

type synchronizationDiagnosticRecorder struct{ writer io.Writer }

func (r synchronizationDiagnosticRecorder) RecordSynchronization(record app.SynchronizationDiagnostic) {
	if r.writer == nil {
		return
	}
	_ = json.NewEncoder(r.writer).Encode(struct {
		Operation      string                            `json:"operation"`
		Classification app.SynchronizationClassification `json:"classification"`
		Observation    time.Time                         `json:"observation"`
		Error          string                            `json:"error,omitempty"`
	}{record.Operation, record.Classification, record.Observation, record.Error})
}

const mappingStepHold = time.Second

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

func runMappingSweep(ctx context.Context, output app.Output, safety render.Safety, backoff app.Backoff, hold func(context.Context) error, diagnostic func(int)) error {
	renderer, err := render.New(stripLength, safety)
	if err != nil {
		return errors.Join(fmt.Errorf("initialize mapping renderer: %w", err), closeUnownedOutput(output))
	}
	worker, err := app.NewWorker(ctx, output, app.WorkerOptions{MaxRetries: 2, Backoff: backoff})
	if err != nil {
		return errors.Join(fmt.Errorf("initialize mapping worker: %w", err), closeUnownedOutput(output))
	}
	for position := 0; position < stripLength; position++ {
		frame, err := renderer.Render(mappingSun(position))
		if err != nil {
			return failTuning(worker, fmt.Errorf("render mapping light %d: %w", position+1, err))
		}
		if err := worker.Submit(frame); err != nil {
			return failTuning(worker, fmt.Errorf("submit mapping light %d: %w", position+1, err))
		}
		if err := waitForDelivery(ctx, worker, frame); err != nil {
			return failTuning(worker, fmt.Errorf("mapping light %d: %w", position+1, err))
		}
		if diagnostic != nil {
			diagnostic(position + 1)
		}
		if err := hold(ctx); err != nil {
			return failTuning(worker, fmt.Errorf("hold mapping light %d: %w", position+1, err))
		}
	}
	if err := worker.Close(); err != nil {
		return fmt.Errorf("finish mapping sweep: %w", err)
	}
	return nil
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
	return mappingSun(stripLength / 2)
}

func mappingSun(position int) render.ArtificialSun {
	return render.ArtificialSun{
		Position:         position,
		Color:            render.Pixel{W: 0x90, R: 0xA0, G: 0x35},
		IntensityProfile: []uint8{32, 64, 128, 255, 255, 255, 128, 64, 32},
	}
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

func holdMappingStep(ctx context.Context) error {
	timer := time.NewTimer(mappingStepHold)
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

type lifecycleRecorder struct {
	mu  sync.Mutex
	out io.Writer
	err io.Writer
}

func (r *lifecycleRecorder) RecordSynchronization(record app.SynchronizationDiagnostic) {
	writer := r.out
	if record.Error != "" {
		writer = r.err
	}
	if writer == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = json.NewEncoder(writer).Encode(struct {
		Operation      string                            `json:"operation"`
		Classification app.SynchronizationClassification `json:"classification"`
		Observation    time.Time                         `json:"observation"`
		Error          string                            `json:"error,omitempty"`
	}{record.Operation, record.Classification, record.Observation, record.Error})
}

func (r *lifecycleRecorder) record(writer io.Writer, operation, status string, err error) {
	if writer == nil {
		return
	}
	record := struct {
		Operation string    `json:"operation"`
		Status    string    `json:"status"`
		Error     string    `json:"error,omitempty"`
		Time      time.Time `json:"time"`
	}{Operation: operation, Status: status, Time: time.Now().UTC()}
	if err != nil {
		record.Error = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = json.NewEncoder(writer).Encode(record)
}

type serviceDependencies struct {
	loadConfig      func(string) (config.Config, error)
	openOutput      func(context.Context, config.Output) (app.Output, error)
	clock           app.SystemClock
	synchronization app.SynchronizationSource
	out             io.Writer
	err             io.Writer
}

func defaultServiceDependencies() serviceDependencies {
	return serviceDependencies{loadConfig: config.Load, openOutput: newDeviceOutput, clock: newSystemClock(), synchronization: timesync.NewTimedatectl(), out: os.Stdout, err: os.Stderr}
}

func runService(ctx context.Context, configPath string, dependencies serviceDependencies) error {
	recorder := &lifecycleRecorder{out: dependencies.out, err: dependencies.err}
	cfg, err := dependencies.loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load startup configuration: %w", err)
	}
	stateStore, err := store.NewFile(cfg.StatePath, cfg.Output.StripLength)
	if err != nil {
		return fmt.Errorf("initialize device state store: %w", err)
	}
	state, err := stateStore.Load(ctx)
	if err != nil {
		return fmt.Errorf("load durable device state: %w", err)
	}
	renderer, err := render.New(cfg.Output.StripLength, cfg.Safety)
	if err != nil {
		return fmt.Errorf("initialize service renderer: %w", err)
	}
	// Prove the actual service scene can be evaluated safely before taking
	// ownership of physical output. A centered sun exercises the full profile.
	if _, err := renderer.Render(mappingSun(cfg.Output.StripLength / 2)); err != nil {
		return fmt.Errorf("validate service scene safety: %w", err)
	}
	output, err := dependencies.openOutput(ctx, cfg.Output)
	if err != nil {
		return errors.Join(fmt.Errorf("initialize physical LED output: %w", err), cleanupOutput(output, cfg.Service.CleanupTimeout))
	}
	// The worker has an explicitly managed lifetime so root cancellation first
	// stops the controller, preventing new submissions before output cleanup.
	worker, err := app.NewWorker(context.Background(), output, app.WorkerOptions{MaxRetries: 2, CleanupTimeout: cfg.Service.CleanupTimeout, Backoff: func(ctx context.Context, attempt int) error {
		t := time.NewTimer(time.Duration(attempt) * 25 * time.Millisecond)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}})
	if err != nil {
		return errors.Join(fmt.Errorf("initialize output worker: %w", err), closeUnownedOutput(output))
	}
	runtime := app.RuntimeOptions{Clock: dependencies.clock, Renderer: renderer, Frames: worker, Sun: mappingSun(0), Synchronization: dependencies.synchronization, Diagnostics: recorder}
	controller, err := app.NewRuntimeController(ctx, state, stateStore, runtime)
	if err != nil {
		return errors.Join(fmt.Errorf("initialize runtime controller: %w", err), worker.Close())
	}

	diagnosticsDone := make(chan struct{})
	diagnosticsStarted := false
	syncCtx, cancelSync := context.WithCancel(ctx)
	syncRequests := make(chan struct{}, 1)
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		for {
			select {
			case <-syncCtx.Done():
				return
			case <-syncRequests:
				_ = controller.RefreshSynchronization(syncCtx)
			}
		}
	}()
	requestSynchronization := func() {
		select {
		case syncRequests <- struct{}{}:
		default:
		}
	}
	shutdown := func(cause error) error {
		cancelSync()
		controller.Close()
		<-syncDone
		cleanupErr := worker.Close()
		if diagnosticsStarted {
			<-diagnosticsDone
		}
		fatalCause := cause != nil && !errors.Is(cause, context.Canceled)
		if cleanupErr != nil || fatalCause {
			recorder.record(dependencies.err, "shutdown", "failed", errors.Join(cause, cleanupErr))
		} else {
			recorder.record(dependencies.out, "shutdown", "stopped", nil)
		}
		if fatalCause {
			return errors.Join(cause, cleanupErr)
		}
		return cleanupErr
	}

	if err := controller.Tick(ctx); err != nil {
		return shutdown(fmt.Errorf("render initial auto tick: %w", err))
	}
	snapshot, err := controller.RuntimeSnapshot(ctx)
	if err != nil {
		return shutdown(fmt.Errorf("inspect initial auto tick: %w", err))
	}
	if err := waitForDelivery(ctx, worker, snapshot.Frame); err != nil {
		return shutdown(fmt.Errorf("deliver initial auto tick: %w", err))
	}
	diagnosticsStarted = true
	go func() {
		defer close(diagnosticsDone)
		for e := range worker.Errors() {
			recorder.record(dependencies.err, "output_delivery", "degraded", e)
		}
	}()
	recorder.record(dependencies.out, "startup", "ready", nil)
	// Synchronization health is diagnostic-only and cannot gate readiness or ticks.
	requestSynchronization()
	ticks := time.NewTicker(cfg.Service.TickInterval)
	defer ticks.Stop()
	syncTicks := time.NewTicker(cfg.Service.SynchronizationInterval)
	defer syncTicks.Stop()
	for {
		select {
		case <-ctx.Done():
			return shutdown(ctx.Err())
		case <-ticks.C:
			if err := controller.Tick(ctx); err != nil && ctx.Err() == nil {
				recorder.record(dependencies.err, "tick", "degraded", err)
			}
		case <-syncTicks.C:
			requestSynchronization()
		}
	}
}

func cleanupOutput(output app.Output, timeout time.Duration) error {
	if output == nil || (reflect.ValueOf(output).Kind() == reflect.Pointer && reflect.ValueOf(output).IsNil()) {
		return nil
	}
	clearCtx, cancelClear := context.WithTimeout(context.Background(), timeout)
	clearErr := output.Clear(clearCtx)
	cancelClear()
	closeCtx, cancelClose := context.WithTimeout(context.Background(), timeout)
	closeErr := output.Close(closeCtx)
	cancelClose()
	if clearErr != nil {
		clearErr = fmt.Errorf("clear output after initialization failure: %w", clearErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close output after initialization failure: %w", closeErr)
	}
	return errors.Join(clearErr, closeErr)
}

func configPathFromArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "/etc/sundial/config.json", nil
	}
	if len(args) == 2 && args[0] == "--config" && args[1] != "" {
		return args[1], nil
	}
	return "", errors.New("usage: sundial [--config PATH]")
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	configPath, err := configPathFromArgs(os.Args[1:])
	if err != nil {
		return err
	}
	return runService(ctx, configPath, defaultServiceDependencies())
}

func main() {
	if err := run(); err != nil {
		data, _ := json.Marshal(struct {
			Operation string `json:"operation"`
			Status    string `json:"status"`
			Error     string `json:"error"`
		}{"startup", "failed", err.Error()})
		_, _ = os.Stderr.Write(append(data, '\n'))
		os.Exit(1)
	}
}
