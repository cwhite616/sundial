package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/clock"
	"github.com/cwhite616/sundial/internal/config"
	"github.com/cwhite616/sundial/internal/render"
)

type steppingClock struct {
	mu   sync.Mutex
	next time.Time
}

func (c *steppingClock) Sample() clock.Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	value := c.next
	c.next = c.next.Add(time.Hour)
	return clock.Sample{Wall: value}
}

type syncFailure struct{}

func (syncFailure) Observe(context.Context) (app.SynchronizationObservation, error) {
	return app.SynchronizationObservation{}, errors.New("timedatectl unavailable")
}

type blockingSynchronization struct {
	mu      sync.Mutex
	calls   int
	active  int
	maximum int
}

func (s *blockingSynchronization) Observe(ctx context.Context) (app.SynchronizationObservation, error) {
	s.mu.Lock()
	s.calls++
	s.active++
	if s.active > s.maximum {
		s.maximum = s.active
	}
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return app.SynchronizationObservation{}, ctx.Err()
}
func (s *blockingSynchronization) snapshot() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.maximum
}

type lifecycleOutput struct {
	mu                       sync.Mutex
	attempts, clears, closes int
	failAfter                int
	cleanupErr               error
	events                   []string
}

func (o *lifecycleOutput) WriteFrame(context.Context, render.Frame) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.attempts++
	o.events = append(o.events, "write")
	if o.failAfter > 0 && o.attempts >= o.failAfter {
		return errors.New("LED write failed")
	}
	return nil
}
func (o *lifecycleOutput) Clear(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clears++
	o.events = append(o.events, "clear")
	return o.cleanupErr
}
func (o *lifecycleOutput) Close(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closes++
	o.events = append(o.events, "close")
	return o.cleanupErr
}
func (o *lifecycleOutput) counts() (int, int, int, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.attempts, o.clears, o.closes, append([]string(nil), o.events...)
}

func serviceTestConfig(t *testing.T) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	document := `{"schema_version":1,"preferred_zone":"UTC","calibration":[{"hour":0,"minute":0,"second":0,"nanosecond":0,"pixel":0},{"hour":12,"minute":0,"second":0,"nanosecond":0,"pixel":12}]}`
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	return config.Config{StatePath: path, Output: config.Output{Driver: "rpi-ws281x", StripLength: 24, NativeBrightness: 255}, Safety: render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 100000, BrightnessCeiling: 255}, Service: config.Service{TickInterval: 10 * time.Millisecond, SynchronizationInterval: 20 * time.Millisecond, CleanupTimeout: time.Second}}
}

func TestRunServiceValidBootRendersImmediatelyTicksAndReportsReady(t *testing.T) {
	cfg := serviceTestConfig(t)
	output := &lifecycleOutput{}
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	deps := serviceDependencies{loadConfig: func(string) (config.Config, error) { return cfg, nil }, openOutput: func(context.Context, config.Output) (app.Output, error) { return output, nil }, clock: &steppingClock{next: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)}, synchronization: syncFailure{}, out: &stdout, err: &stderr}
	done := make(chan error, 1)
	go func() { done <- runService(ctx, "ignored", deps) }()
	deadline := time.After(time.Second)
	for {
		attempts, _, _, _ := output.counts()
		if attempts >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("periodic tick not delivered")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var ready map[string]any
	if err := json.Unmarshal(bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))[0], &ready); err != nil {
		t.Fatalf("ready is not NDJSON: %v: %s", err, stdout.String())
	}
	if ready["operation"] != "startup" || ready["status"] != "ready" {
		t.Fatalf("ready record=%v", ready)
	}
}

func TestRunServiceRejectsUnsafeStartupBeforeOutputAndCleansOwnedOutput(t *testing.T) {
	var opened bool
	deps := serviceDependencies{loadConfig: func(string) (config.Config, error) { return config.Config{}, errors.New("unsafe config") }, openOutput: func(context.Context, config.Output) (app.Output, error) {
		opened = true
		return &lifecycleOutput{}, nil
	}}
	if err := runService(context.Background(), "bad", deps); err == nil || opened {
		t.Fatalf("err=%v opened=%v", err, opened)
	}
	cfg := serviceTestConfig(t)
	owned := &lifecycleOutput{}
	deps = serviceDependencies{loadConfig: func(string) (config.Config, error) { return cfg, nil }, openOutput: func(context.Context, config.Output) (app.Output, error) { return owned, nil }, clock: nil}
	if err := runService(context.Background(), "bad-runtime", deps); err == nil {
		t.Fatal("expected runtime startup error")
	}
	_, clears, closes, _ := owned.counts()
	if clears != 1 || closes != 1 {
		t.Fatalf("owned cleanup clear=%d close=%d", clears, closes)
	}
}

func TestRunServiceDegradedSynchronizationAndOutputKeepTicking(t *testing.T) {
	cfg := serviceTestConfig(t)
	output := &lifecycleOutput{failAfter: 2}
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	deps := serviceDependencies{loadConfig: func(string) (config.Config, error) { return cfg, nil }, openOutput: func(context.Context, config.Output) (app.Output, error) { return output, nil }, clock: &steppingClock{next: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)}, synchronization: syncFailure{}, err: &stderr}
	_ = runService(ctx, "ignored", deps)
	attempts, _, _, _ := output.counts()
	if attempts < 4 {
		t.Fatalf("tick loop stopped after degraded output: attempts=%d", attempts)
	}
	if !strings.Contains(stderr.String(), `"operation":"refresh_synchronization"`) || !strings.Contains(stderr.String(), `"operation":"output_delivery"`) {
		t.Fatalf("missing degraded NDJSON: %s", stderr.String())
	}
	for _, line := range bytes.Split(bytes.TrimSpace(stderr.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid NDJSON %q: %v", line, err)
		}
	}
}

func TestRunServiceDoesNotOverlapSlowSynchronizationRefreshes(t *testing.T) {
	cfg := serviceTestConfig(t)
	output := &lifecycleOutput{}
	source := &blockingSynchronization{}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	deps := serviceDependencies{loadConfig: func(string) (config.Config, error) { return cfg, nil }, openOutput: func(context.Context, config.Output) (app.Output, error) { return output, nil }, clock: &steppingClock{next: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)}, synchronization: source}
	_ = runService(ctx, "ignored", deps)
	calls, maximum := source.snapshot()
	if calls != 1 || maximum != 1 {
		t.Fatalf("blocking synchronization calls=%d maximum concurrency=%d", calls, maximum)
	}
}

func TestRunServiceCleansPartiallyConstructedOutputAndPreservesErrors(t *testing.T) {
	cfg := serviceTestConfig(t)
	initErr := errors.New("native init failed")
	cleanupErr := errors.New("release failed")
	output := &lifecycleOutput{cleanupErr: cleanupErr}
	deps := serviceDependencies{loadConfig: func(string) (config.Config, error) { return cfg, nil }, openOutput: func(context.Context, config.Output) (app.Output, error) { return output, initErr }}
	err := runService(context.Background(), "ignored", deps)
	if !errors.Is(err, initErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("errors not preserved: %v", err)
	}
	_, clears, closes, events := output.counts()
	if clears != 1 || closes != 1 || events[0] != "clear" || events[1] != "close" {
		t.Fatalf("partial output cleanup: clear=%d close=%d events=%v", clears, closes, events)
	}
}

func TestConfigPathFromArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{{"default", nil, "/etc/sundial/config.json", false}, {"explicit", []string{"--config", "/tmp/test.json"}, "/tmp/test.json", false}, {"missing path", []string{"--config"}, "", true}, {"empty path", []string{"--config", ""}, "", true}, {"unknown", []string{"--verbose"}, "", true}, {"extra", []string{"--config", "a", "b"}, "", true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configPathFromArgs(tt.args)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("got=(%q,%v), want=(%q,error=%v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestLifecycleSynchronizationDiagnosticsRouteSuccessAndFailureOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	recorder := &lifecycleRecorder{out: &stdout, err: &stderr}
	observed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recorder.RecordSynchronization(app.SynchronizationDiagnostic{Operation: "refresh_synchronization", Classification: app.SynchronizationSynchronized, Observation: observed})
	recorder.RecordSynchronization(app.SynchronizationDiagnostic{Operation: "refresh_synchronization", Classification: app.SynchronizationStale, Observation: observed, Error: "probe failed"})
	if bytes.Count(stdout.Bytes(), []byte("\n")) != 1 || bytes.Count(stderr.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("routing stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, line := range [][]byte{bytes.TrimSpace(stdout.Bytes()), bytes.TrimSpace(stderr.Bytes())} {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid NDJSON %q: %v", line, err)
		}
		if record["operation"] != "refresh_synchronization" {
			t.Fatalf("record=%v", record)
		}
	}
}

func TestRunServiceCancellationClearsThenClosesOnceAndPreservesCleanupError(t *testing.T) {
	cfg := serviceTestConfig(t)
	cleanupErr := errors.New("cleanup failed")
	output := &lifecycleOutput{cleanupErr: cleanupErr}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Cancellation before ownership is intentionally fail-dark and does not construct output.
	opened := false
	deps := serviceDependencies{loadConfig: func(string) (config.Config, error) { return cfg, nil }, openOutput: func(context.Context, config.Output) (app.Output, error) { opened = true; return output, nil }, clock: &steppingClock{next: time.Now()}, synchronization: syncFailure{}}
	_ = runService(ctx, "ignored", deps)
	if opened {
		t.Fatal("output owned after pre-start cancellation")
	}
	ctx, cancel = context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	deps.out = &stdout
	deps.err = &stderr
	done := make(chan error, 1)
	go func() { done <- runService(ctx, "ignored", deps) }()
	for {
		a, _, _, _ := output.counts()
		if a > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	err := <-done
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup error not preserved: %v", err)
	}
	_, clears, closes, events := output.counts()
	if clears != 1 || closes != 1 {
		t.Fatalf("clear=%d close=%d", clears, closes)
	}
	if events[len(events)-2] != "clear" || events[len(events)-1] != "close" {
		t.Fatalf("shutdown order=%v", events)
	}
}

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
	if nativeDriverBrightness != 255 || rendererBrightnessCeiling != 255 {
		t.Fatalf("brightness controls: native=%d renderer=%d", nativeDriverBrightness, rendererBrightnessCeiling)
	}
}

func TestRuntimeControllerOptionsWireSystemClockAndFrameDelivery(t *testing.T) {
	renderer, err := render.New(1, render.Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 1000, BrightnessCeiling: 255})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := app.NewWorker(context.Background(), &previewOutput{}, app.WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	options := runtimeControllerOptions(renderer, worker, render.ArtificialSun{Color: render.Pixel{W: 255}})
	first, second := options.Clock.Sample(), options.Clock.Sample()
	if options.Renderer != renderer || options.Frames != worker || options.Synchronization == nil || options.Diagnostics == nil || second.Monotonic < first.Monotonic || first.Wall.IsZero() || second.Wall.IsZero() {
		t.Fatalf("runtime options not wired: %+v", options)
	}
}

func TestSynchronizationDiagnosticHasStableStructuredFields(t *testing.T) {
	var output bytes.Buffer
	recorder := synchronizationDiagnosticRecorder{writer: &output}
	recorder.RecordSynchronization(app.SynchronizationDiagnostic{
		Operation: "refresh_synchronization", Classification: app.SynchronizationStale,
		Observation: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC), Error: "timedatectl failed",
	})
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"operation", "classification", "observation", "error"} {
		if _, ok := record[field]; !ok {
			t.Fatalf("diagnostic missing %q: %s", field, output.String())
		}
	}
	if got := record["operation"]; got != "refresh_synchronization" {
		t.Fatalf("operation = %v", got)
	}
	if got := record["classification"]; got != "stale" {
		t.Fatalf("classification = %v", got)
	}
	if got := record["observation"]; got != "2026-08-20T12:00:00Z" {
		t.Fatalf("observation = %v", got)
	}
	if got := record["error"]; got != "timedatectl failed" {
		t.Fatalf("error = %v", got)
	}
	if !strings.HasSuffix(output.String(), "\n") || strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("diagnostic is not one newline-delimited record: %q", output.String())
	}
}

func TestSynchronizationDiagnosticNilWriterIsSafe(t *testing.T) {
	synchronizationDiagnosticRecorder{}.RecordSynchronization(app.SynchronizationDiagnostic{})
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
	want := []render.Pixel{{R: 255}, {G: 255}, {B: 255}, {W: 255}, {W: 0x90, R: 0xA0, G: 0x35}}
	for i, frame := range writes {
		pixel, _ := frame.Pixel(stripLength / 2)
		if pixel != want[i] {
			t.Fatalf("bounded frame %d = %+v, want %+v", i, pixel, want[i])
		}
	}
	final := writes[len(writes)-1]
	wantIntensity := map[int]uint8{68: 32, 69: 64, 70: 128, 71: 255, 72: 255, 73: 255, 74: 128, 75: 64, 76: 32}
	for position, pixel := range final.Pixels() {
		intensity := wantIntensity[position]
		want := render.Pixel{W: uint8(uint16(0x90) * uint16(intensity) / 255), R: uint8(uint16(0xA0) * uint16(intensity) / 255), G: uint8(uint16(0x35) * uint16(intensity) / 255)}
		if pixel != want {
			t.Fatalf("final glow pixel %d = %+v, want %+v", position, pixel, want)
		}
	}
}

func TestMappingSweepDeliversEveryOneBasedLightAndFailsDark(t *testing.T) {
	output := &previewOutput{}
	var lights []int
	var holds int
	err := runMappingSweep(context.Background(), output, previewSafety, nil, func(context.Context) error {
		holds++
		return nil
	}, func(light int) {
		lights = append(lights, light)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lights) != stripLength || holds != stripLength || lights[0] != 1 || lights[stripLength-1] != 144 {
		t.Fatalf("mapping progress: lights=%v holds=%d", lights, holds)
	}
	output.mu.Lock()
	writes, clears, closes := append([]render.Frame(nil), output.writes...), output.clears, output.closes
	output.mu.Unlock()
	if len(writes) != stripLength || clears != 1 || closes != 1 {
		t.Fatalf("mapping lifecycle: writes=%d clears=%d closes=%d", len(writes), clears, closes)
	}
	first, _ := writes[0].Pixel(0)
	last, _ := writes[stripLength-1].Pixel(stripLength - 1)
	full := render.Pixel{W: 0x90, R: 0xA0, G: 0x35}
	if first != full || last != full {
		t.Fatalf("mapping boundaries: first=%+v last=%+v want=%+v", first, last, full)
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

func TestTuningSequenceEvaluatesZoneExactBetweenAndMidnightTrials(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	if err != nil {
		t.Fatal(err)
	}
	at := func(hour, minute int) clock.TimeOfDay {
		value, err := clock.NewTimeOfDay(hour, minute, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	calibration, err := clock.NewCalibration(stripLength, []clock.CalibrationPoint{
		{Time: at(1, 0), Pixel: 20},
		{Time: at(8, 0), Pixel: 100},
		{Time: at(10, 0), Pixel: 80},
		{Time: at(23, 0), Pixel: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	// These are absolute instants; Detroit local time is UTC-4 on this date.
	trials := []tuningTrial{
		{ID: "exact", Instant: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC), Color: render.Pixel{W: 255}},
		{ID: "descending-between", Instant: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC), Color: render.Pixel{W: 255}},
		{ID: "midnight", Instant: time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC), Color: render.Pixel{W: 255}},
	}
	output := &previewOutput{}
	var results []tuningResult
	worker, err := startTuningSequence(context.Background(), output, previewSafety, nil, calibration, detroit, trials, func(result tuningResult) error {
		results = append(results, result)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	wantPositions := []int{100, 90, 30}
	for i, result := range results {
		if result.ID != trials[i].ID || result.Position != wantPositions[i] {
			t.Fatalf("result %d = %+v, want id %q position %d", i, result, trials[i].ID, wantPositions[i])
		}
		pixel, pixelErr := result.Frame.Pixel(result.Position)
		if pixelErr != nil || pixel != (render.Pixel{W: 255}) || result.Frame.Len() != stripLength {
			t.Fatalf("result %d unsafe or malformed frame: len=%d pixel=%+v err=%v", i, result.Frame.Len(), pixel, pixelErr)
		}
	}
	if results[0].IntervalFrom != results[0].IntervalTo || results[1].CrossesMidnight || !results[2].CrossesMidnight {
		t.Fatalf("interval attribution: exact=%+v between=%+v midnight=%+v", results[0], results[1], results[2])
	}
}

func TestTuningSequenceFailsDarkWithContext(t *testing.T) {
	pointA, _ := clock.NewTimeOfDay(8, 0, 0, 0)
	pointB, _ := clock.NewTimeOfDay(10, 0, 0, 0)
	calibration, err := clock.NewCalibration(stripLength, []clock.CalibrationPoint{{Time: pointA, Pixel: 10}, {Time: pointB, Pixel: 20}})
	if err != nil {
		t.Fatal(err)
	}
	trial := []tuningTrial{{ID: "exact", Instant: time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC), Color: render.Pixel{R: 255}}}

	output := &previewOutput{}
	_, err = startTuningSequence(context.Background(), output, previewSafety, nil, calibration, nil, trial, nil)
	if !errors.Is(err, clock.ErrInvalidLocation) || !strings.Contains(err.Error(), `evaluate trial "exact"`) {
		t.Fatalf("location error = %v", err)
	}
	output.mu.Lock()
	clears, closes := output.clears, output.closes
	output.mu.Unlock()
	if clears != 1 || closes != 1 {
		t.Fatalf("location cleanup: clears=%d closes=%d", clears, closes)
	}

	output = &previewOutput{}
	_, err = startTuningSequence(context.Background(), output, previewSafety, nil, clock.Calibration{}, time.UTC, trial, nil)
	if !errors.Is(err, clock.ErrInvalidStripLength) || !strings.Contains(err.Error(), `evaluate trial "exact"`) {
		t.Fatalf("calibration error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output = &previewOutput{}
	_, err = startTuningSequence(ctx, output, previewSafety, nil, calibration, time.UTC, trial, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}

	diagnosticErr := errors.New("record failed")
	output = &previewOutput{}
	_, err = startTuningSequence(context.Background(), output, previewSafety, nil, calibration, time.UTC, trial, func(tuningResult) error { return diagnosticErr })
	if !errors.Is(err, diagnosticErr) {
		t.Fatalf("diagnostic error = %v", err)
	}
}
