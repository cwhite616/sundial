package timesync

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/cwhite616/sundial/internal/app"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).Output()
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
	}
	return nil, err
}

type Timedatectl struct {
	runner CommandRunner
	now    func() time.Time
}

func NewTimedatectl() *Timedatectl {
	return &Timedatectl{runner: execRunner{}, now: time.Now}
}

func NewTimedatectlWithRunner(runner CommandRunner, now func() time.Time) (*Timedatectl, error) {
	if isNilInterface(runner) {
		return nil, errors.New("initialize timedatectl synchronization source: nil command runner")
	}
	if now == nil {
		return nil, errors.New("initialize timedatectl synchronization source: nil observation clock")
	}
	return &Timedatectl{runner: runner, now: now}, nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func (t *Timedatectl) Observe(ctx context.Context) (app.SynchronizationObservation, error) {
	if ctx == nil {
		return app.SynchronizationObservation{}, errors.New("query timedatectl synchronization: nil context")
	}
	if t == nil || isNilInterface(t.runner) || t.now == nil {
		return app.SynchronizationObservation{}, errors.New("query timedatectl synchronization: source is not initialized")
	}
	output, err := t.runner.Run(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
	if err != nil {
		return app.SynchronizationObservation{}, fmt.Errorf("query timedatectl synchronization status: %w", err)
	}
	value := strings.TrimSpace(string(output))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return app.SynchronizationObservation{}, fmt.Errorf("parse timedatectl synchronization status %q: malformed output", value)
	}
	classification := app.SynchronizationUnknown
	switch strings.ToLower(value) {
	case "yes":
		classification = app.SynchronizationSynchronized
	case "no":
		classification = app.SynchronizationUnsynchronized
	}
	return app.SynchronizationObservation{Classification: classification, ObservedAt: t.now()}, nil
}
