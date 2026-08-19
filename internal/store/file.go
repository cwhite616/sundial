package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/clock"
)

const currentSchemaVersion = 1

var (
	ErrInvalidDocument   = errors.New("invalid device state document")
	ErrUnsupportedSchema = errors.New("unsupported device state schema")
)

type File struct {
	path        string
	stripLength int
}

func NewFile(path string, stripLength int) (*File, error) {
	if path == "" {
		return nil, errors.New("initialize device state file: empty path")
	}
	if stripLength <= 0 {
		return nil, fmt.Errorf("initialize device state file: %w: %d", clock.ErrInvalidStripLength, stripLength)
	}
	return &File{path: path, stripLength: stripLength}, nil
}

type document struct {
	SchemaVersion int             `json:"schema_version"`
	PreferredZone string          `json:"preferred_zone"`
	Calibration   []documentPoint `json:"calibration"`
}

type documentPoint struct {
	Hour       int `json:"hour"`
	Minute     int `json:"minute"`
	Second     int `json:"second"`
	Nanosecond int `json:"nanosecond"`
	Pixel      int `json:"pixel"`
}

func (f *File) Load(ctx context.Context) (app.DeviceState, error) {
	if err := contextError(ctx, "load device state"); err != nil {
		return app.DeviceState{}, err
	}
	file, err := os.Open(f.path)
	if err != nil {
		return app.DeviceState{}, fmt.Errorf("open device state %q: %w", f.path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return app.DeviceState{}, fmt.Errorf("read device state %q: %w", f.path, err)
	}
	if err := validateDocumentJSON(data); err != nil {
		return app.DeviceState{}, fmt.Errorf("decode device state %q: %w", f.path, errors.Join(ErrInvalidDocument, err))
	}
	var stored document
	if err := json.Unmarshal(data, &stored); err != nil {
		return app.DeviceState{}, fmt.Errorf("decode device state %q: %w", f.path, errors.Join(ErrInvalidDocument, err))
	}
	if stored.SchemaVersion != currentSchemaVersion {
		return app.DeviceState{}, fmt.Errorf("load device state %q: %w: %d", f.path, ErrUnsupportedSchema, stored.SchemaVersion)
	}

	points := make([]clock.CalibrationPoint, len(stored.Calibration))
	for i, point := range stored.Calibration {
		value, err := clock.NewTimeOfDay(point.Hour, point.Minute, point.Second, point.Nanosecond)
		if err != nil {
			return app.DeviceState{}, fmt.Errorf("load device state %q calibration point %d: %w", f.path, i, err)
		}
		points[i] = clock.CalibrationPoint{Time: value, Pixel: point.Pixel}
	}
	state, err := app.NewDeviceState(0, app.Replacement{
		PreferredZone: stored.PreferredZone,
		StripLength:   f.stripLength,
		Points:        points,
	})
	if err != nil {
		return app.DeviceState{}, fmt.Errorf("validate device state %q: %w", f.path, err)
	}
	if err := contextError(ctx, "load device state"); err != nil {
		return app.DeviceState{}, err
	}
	return state, nil
}

func (f *File) Save(ctx context.Context, state app.DeviceState) error {
	if err := contextError(ctx, "save device state"); err != nil {
		return err
	}
	validated, err := app.NewDeviceState(state.Revision(), app.Replacement{
		PreferredZone: state.PreferredZone(),
		StripLength:   state.Calibration().StripLength(),
		Points:        state.Calibration().Points(),
	})
	if err != nil || validated.Calibration().StripLength() != f.stripLength {
		if err == nil {
			err = fmt.Errorf("%w: state has %d, store requires %d", clock.ErrInvalidStripLength, validated.Calibration().StripLength(), f.stripLength)
		}
		return fmt.Errorf("validate device state for %q: %w", f.path, err)
	}

	stored := document{SchemaVersion: currentSchemaVersion, PreferredZone: validated.PreferredZone()}
	for _, point := range validated.Calibration().Points() {
		stored.Calibration = append(stored.Calibration, documentPoint{
			Hour: point.Time.Hour(), Minute: point.Time.Minute(), Second: point.Time.Second(),
			Nanosecond: point.Time.Nanosecond(), Pixel: point.Pixel,
		})
	}
	return f.replace(ctx, stored)
}

func (f *File) replace(ctx context.Context, stored document) (resultErr error) {
	directory := filepath.Dir(f.path)
	temporary, err := os.CreateTemp(directory, ".sundial-state-*")
	if err != nil {
		return fmt.Errorf("create temporary device state beside %q: %w", f.path, err)
	}
	temporaryPath := temporary.Name()
	renamed := false
	defer func() {
		if closeErr := temporary.Close(); closeErr != nil && resultErr == nil && !renamed {
			resultErr = fmt.Errorf("close temporary device state %q: %w", temporaryPath, closeErr)
		}
		if !renamed {
			if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && resultErr == nil {
				resultErr = fmt.Errorf("remove temporary device state %q: %w", temporaryPath, removeErr)
			}
		}
	}()

	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(stored); err != nil {
		return fmt.Errorf("encode temporary device state %q: %w", temporaryPath, err)
	}
	if err := contextError(ctx, "save device state"); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary device state %q: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary device state %q: %w", temporaryPath, err)
	}
	if err := contextError(ctx, "save device state"); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, f.path); err != nil {
		return fmt.Errorf("replace device state %q: %w", f.path, err)
	}
	renamed = true
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync device state directory %q: %w", directory, err)
	}
	return nil
}

func validateDocumentJSON(data []byte) error {
	return validateExactObject(data, []string{"schema_version", "preferred_zone", "calibration"}, map[string]func(json.RawMessage) error{
		"calibration": func(raw json.RawMessage) error {
			var points []json.RawMessage
			if err := json.Unmarshal(raw, &points); err != nil {
				return err
			}
			if points == nil {
				return errors.New("calibration must be an array")
			}
			for i, point := range points {
				if err := validateExactObject(point, []string{"hour", "minute", "second", "nanosecond", "pixel"}, nil); err != nil {
					return fmt.Errorf("calibration point %d: %w", i, err)
				}
			}
			return nil
		},
	})
}

func validateExactObject(data []byte, required []string, validators map[string]func(json.RawMessage) error) error {
	allowed := make(map[string]bool, len(required))
	for _, name := range required {
		allowed[name] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("expected JSON object")
	}
	seen := make(map[string]bool, len(required))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok || !allowed[name] {
			return fmt.Errorf("unknown field %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("field %q must not be null", name)
		}
		if validate := validators[name]; validate != nil {
			if err := validate(raw); err != nil {
				return fmt.Errorf("field %q: %w", name, err)
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	for _, name := range required {
		if !seen[name] {
			return fmt.Errorf("missing field %q", name)
		}
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing token %v", token)
	}
	return nil
}

func contextError(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s: nil context", operation)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
