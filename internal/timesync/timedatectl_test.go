package timesync

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwhite616/sundial/internal/app"
)

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

type nilRunner struct{}

func (*nilRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, nil }

func (f runnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func TestTimedatectlObserveClassifiesStatusOnlyQuery(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		output string
		want   app.SynchronizationClassification
	}{
		{"yes\n", app.SynchronizationSynchronized},
		{"no\n", app.SynchronizationUnsynchronized},
		{"n/a\n", app.SynchronizationUnknown},
	} {
		t.Run(tc.output, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			source, err := NewTimedatectlWithRunner(runnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
				gotName, gotArgs = name, append([]string(nil), args...)
				return []byte(tc.output), nil
			}), func() time.Time { return observedAt })
			if err != nil {
				t.Fatal(err)
			}
			observation, err := source.Observe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if observation.Classification != tc.want || observation.ObservedAt != observedAt {
				t.Fatalf("observation = %+v", observation)
			}
			wantArgs := []string{"show", "--property=NTPSynchronized", "--value"}
			if gotName != "timedatectl" || !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("command = %q %v, want timedatectl %v", gotName, gotArgs, wantArgs)
			}
		})
	}
}

func TestTimedatectlObserveContextualizesMalformedFailureAndCancellation(t *testing.T) {
	for _, output := range []string{"", "yes\nno\n"} {
		source, _ := NewTimedatectlWithRunner(runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
			return []byte(output), nil
		}), time.Now)
		if _, err := source.Observe(context.Background()); err == nil {
			t.Fatalf("malformed output %q succeeded", output)
		}
	}

	want := errors.New("command failed")
	source, _ := NewTimedatectlWithRunner(runnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		return nil, want
	}), time.Now)
	if _, err := source.Observe(context.Background()); !errors.Is(err, want) {
		t.Fatalf("command error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source, _ = NewTimedatectlWithRunner(runnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, ctx.Err()
	}), time.Now)
	if _, err := source.Observe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestNewTimedatectlRejectsTypedNilRunner(t *testing.T) {
	var runner *nilRunner
	if _, err := NewTimedatectlWithRunner(runner, time.Now); err == nil {
		t.Fatal("typed-nil runner was accepted")
	}
}

func TestTimedatectlObserveRejectsUninitializedReceiver(t *testing.T) {
	for _, source := range []*Timedatectl{nil, &Timedatectl{}} {
		if _, err := source.Observe(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialized") {
			t.Fatalf("uninitialized source error = %v", err)
		}
	}
}

func TestExecRunnerPreservesCommandStderr(t *testing.T) {
	_, err := (execRunner{}).Run(context.Background(), "sh", "-c", "printf 'system bus unavailable' >&2; exit 7")
	if err == nil || !strings.Contains(err.Error(), "system bus unavailable") {
		t.Fatalf("command error = %v", err)
	}
}
