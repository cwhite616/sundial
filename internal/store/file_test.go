package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/clock"
)

func TestFileSaveAndLoadRoundTrip(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "device-state.json")
	storage, err := NewFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	state := storeState(t, 12, "America/Detroit", 1, 8)
	if err := storage.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"schema_version": 1`) || !strings.Contains(text, `"preferred_zone": "America/Detroit"`) {
		t.Fatalf("saved document lacks version or zone: %s", text)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".sundial-state-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files remain: %v, %v", matches, err)
	}
	loaded, err := storage.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision() != 0 || loaded.PreferredZone() != state.PreferredZone() {
		t.Fatalf("loaded metadata = revision %d zone %q", loaded.Revision(), loaded.PreferredZone())
	}
	if got, want := loaded.Calibration().Points(), state.Calibration().Points(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("loaded points = %+v; want %+v", got, want)
	}
}

func TestFileLoadRejectsInvalidDocumentsWithoutPartialState(t *testing.T) {
	tests := []struct {
		name     string
		document string
		want     error
	}{
		{"malformed", `{`, ErrInvalidDocument},
		{"trailing value", `{"schema_version":1,"preferred_zone":"UTC","calibration":[]} {}`, ErrInvalidDocument},
		{"unknown field", `{"schema_version":1,"preferred_zone":"UTC","calibration":[],"extra":true}`, ErrInvalidDocument},
		{"case variant field", `{"SCHEMA_VERSION":1,"preferred_zone":"UTC","calibration":[]}`, ErrInvalidDocument},
		{"duplicate field", `{"schema_version":1,"schema_version":1,"preferred_zone":"UTC","calibration":[]}`, ErrInvalidDocument},
		{"missing field", `{"schema_version":1,"preferred_zone":"UTC"}`, ErrInvalidDocument},
		{"null field", `{"schema_version":1,"preferred_zone":null,"calibration":[]}`, ErrInvalidDocument},
		{"wrong field type", `{"schema_version":"one","preferred_zone":"UTC","calibration":[]}`, ErrInvalidDocument},
		{"duplicate point field", `{"schema_version":1,"preferred_zone":"UTC","calibration":[{"hour":6,"hour":7,"minute":0,"second":0,"nanosecond":0,"pixel":1},{"hour":18,"minute":0,"second":0,"nanosecond":0,"pixel":8}]}`, ErrInvalidDocument},
		{"unsupported version", `{"schema_version":2,"preferred_zone":"UTC","calibration":[]}`, ErrUnsupportedSchema},
		{"unknown zone", validDocument("Mars/Olympus", 1, 8), app.ErrInvalidPreferredZone},
		{"too few points", `{"schema_version":1,"preferred_zone":"UTC","calibration":[{"hour":6,"minute":0,"second":0,"nanosecond":0,"pixel":1}]}`, clock.ErrTooFewCalibrationPoints},
		{"invalid time", `{"schema_version":1,"preferred_zone":"UTC","calibration":[{"hour":24,"minute":0,"second":0,"nanosecond":0,"pixel":1},{"hour":18,"minute":0,"second":0,"nanosecond":0,"pixel":8}]}`, clock.ErrInvalidTimeOfDay},
		{"invalid pixel", validDocument("UTC", 1, 10), clock.ErrPixelOutOfRange},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "device-state.json")
			if err := os.WriteFile(path, []byte(test.document), 0o600); err != nil {
				t.Fatal(err)
			}
			storage, err := NewFile(path, 10)
			if err != nil {
				t.Fatal(err)
			}
			state, err := storage.Load(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
			if state.Revision() != 0 || state.PreferredZone() != "" || state.Location() != nil || state.Calibration().StripLength() != 0 || state.Calibration().Points() != nil {
				t.Fatalf("invalid document returned partial state: %+v", state)
			}
		})
	}
}

func TestFileLoadPreservesJSONErrorIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":!}`), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = storage.Load(context.Background())
	var syntaxError *json.SyntaxError
	if !errors.Is(err, ErrInvalidDocument) || !errors.As(err, &syntaxError) {
		t.Fatalf("error = %v; want document sentinel and JSON syntax cause", err)
	}
}

func TestCanceledSavePreservesPriorDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-state.json")
	storage, err := NewFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Save(context.Background(), storeState(t, 0, "UTC", 1, 8)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := storage.Save(ctx, storeState(t, 1, "America/Detroit", 2, 7)); !errors.Is(err, context.Canceled) {
		t.Fatalf("save error = %v; want context cancellation", err)
	}
	loaded, err := storage.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PreferredZone() != "UTC" || loaded.Calibration().Points()[0].Pixel != 1 {
		t.Fatalf("canceled save replaced prior state: zone=%q points=%+v", loaded.PreferredZone(), loaded.Calibration().Points())
	}
}

type cancelOnErrCall struct {
	mu       sync.Mutex
	calls    int
	cancelAt int
	done     chan struct{}
	once     sync.Once
}

func newCancelOnErrCall(cancelAt int) *cancelOnErrCall {
	return &cancelOnErrCall{cancelAt: cancelAt, done: make(chan struct{})}
}

func (c *cancelOnErrCall) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelOnErrCall) Done() <-chan struct{}       { return c.done }
func (c *cancelOnErrCall) Value(any) any               { return nil }
func (c *cancelOnErrCall) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls < c.cancelAt {
		return nil
	}
	c.once.Do(func() { close(c.done) })
	return context.Canceled
}

func TestCancellationDuringSavePreservesPriorDocumentAndCleansTemporaryFile(t *testing.T) {
	for _, cancelAt := range []int{2, 3} {
		t.Run("boundary-"+strconv.Itoa(cancelAt), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "device-state.json")
			storage, err := NewFile(path, 10)
			if err != nil {
				t.Fatal(err)
			}
			if err := storage.Save(context.Background(), storeState(t, 0, "UTC", 1, 8)); err != nil {
				t.Fatal(err)
			}
			if err := storage.Save(newCancelOnErrCall(cancelAt), storeState(t, 1, "America/Detroit", 2, 7)); !errors.Is(err, context.Canceled) {
				t.Fatalf("save error = %v; want context cancellation", err)
			}
			loaded, err := storage.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if loaded.PreferredZone() != "UTC" || loaded.Calibration().Points()[0].Pixel != 1 {
				t.Fatalf("mid-save cancellation replaced prior state: zone=%q points=%+v", loaded.PreferredZone(), loaded.Calibration().Points())
			}
			matches, err := filepath.Glob(filepath.Join(directory, ".sundial-state-*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary files remain: %v, %v", matches, err)
			}
		})
	}
}

func TestFileRejectsInvalidSaveState(t *testing.T) {
	storage, err := NewFile(filepath.Join(t.TempDir(), "device-state.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Save(context.Background(), app.DeviceState{}); err == nil {
		t.Fatal("zero state saved")
	}
	wrongLength := storeStateWithLength(t, 0, "UTC", 11, 1, 8)
	if err := storage.Save(context.Background(), wrongLength); !errors.Is(err, clock.ErrInvalidStripLength) {
		t.Fatalf("strip mismatch error = %v; want ErrInvalidStripLength", err)
	}
}

func storeState(t *testing.T, revision uint64, zone string, firstPixel, secondPixel int) app.DeviceState {
	return storeStateWithLength(t, revision, zone, 10, firstPixel, secondPixel)
}

func storeStateWithLength(t *testing.T, revision uint64, zone string, stripLength, firstPixel, secondPixel int) app.DeviceState {
	t.Helper()
	first, err := clock.NewTimeOfDay(6, 7, 8, 9)
	if err != nil {
		t.Fatal(err)
	}
	second, err := clock.NewTimeOfDay(18, 17, 16, 15)
	if err != nil {
		t.Fatal(err)
	}
	state, err := app.NewDeviceState(revision, app.Replacement{PreferredZone: zone, StripLength: stripLength, Points: []clock.CalibrationPoint{{Time: first, Pixel: firstPixel}, {Time: second, Pixel: secondPixel}}})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func validDocument(zone string, firstPixel, secondPixel int) string {
	return `{"schema_version":1,"preferred_zone":"` + zone + `","calibration":[` +
		`{"hour":6,"minute":0,"second":0,"nanosecond":0,"pixel":` + strconv.Itoa(firstPixel) + `},` +
		`{"hour":18,"minute":0,"second":0,"nanosecond":0,"pixel":` + strconv.Itoa(secondPixel) + `}]}`
}
