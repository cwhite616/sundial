package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/cwhite616/sundial/internal/render"
)

const (
	minTickInterval = 10 * time.Millisecond
	maxTickInterval = time.Minute
	maxSyncInterval = 24 * time.Hour
	maxCleanup      = 30 * time.Second
	maxDocumentSize = 64 * 1024
	maxStripLength  = 512
)

type Output struct {
	Driver           string `json:"driver"`
	StripLength      int    `json:"strip_length"`
	NativeBrightness int    `json:"native_brightness"`
}

type Service struct {
	TickInterval            time.Duration
	SynchronizationInterval time.Duration
	CleanupTimeout          time.Duration
}

type Config struct {
	StatePath string
	Output    Output
	Safety    render.Safety
	Service   Service
}

type document struct {
	StatePath string `json:"state_path"`
	Output    Output `json:"output"`
	Safety    struct {
		RedCurrent        uint64 `json:"red_current"`
		GreenCurrent      uint64 `json:"green_current"`
		BlueCurrent       uint64 `json:"blue_current"`
		WhiteCurrent      uint64 `json:"white_current"`
		MaxStripCurrent   uint64 `json:"max_strip_current"`
		BrightnessCeiling int    `json:"brightness_ceiling"`
	} `json:"safety"`
	Service struct {
		TickInterval            string `json:"tick_interval"`
		SynchronizationInterval string `json:"synchronization_interval"`
		CleanupTimeout          string `json:"cleanup_timeout"`
	} `json:"service"`
}

func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("load startup configuration: empty path")
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open startup configuration %q: %w", path, err)
	}
	defer f.Close()
	c, err := Decode(f)
	if err != nil {
		return Config{}, fmt.Errorf("decode startup configuration %q: %w", path, err)
	}
	return c, nil
}

func Decode(r io.Reader) (Config, error) {
	if r == nil || (reflect.ValueOf(r).Kind() == reflect.Pointer && reflect.ValueOf(r).IsNil()) {
		return Config{}, errors.New("read startup configuration: nil reader")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxDocumentSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read startup configuration: %w", err)
	}
	if len(data) > maxDocumentSize {
		return Config{}, fmt.Errorf("startup configuration exceeds %d bytes", maxDocumentSize)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Config{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d document
	if err := dec.Decode(&d); err != nil {
		return Config{}, err
	}
	if err := ensureEOF(dec); err != nil {
		return Config{}, err
	}
	if d.StatePath == "" || !filepath.IsAbs(d.StatePath) {
		return Config{}, errors.New("state_path must be an absolute path")
	}
	if d.Output.Driver != "rpi-ws281x" {
		return Config{}, fmt.Errorf("output.driver must be %q", "rpi-ws281x")
	}
	if d.Output.StripLength <= 0 || d.Output.StripLength > maxStripLength {
		return Config{}, fmt.Errorf("output.strip_length must be in [1,%d]", maxStripLength)
	}
	if d.Output.NativeBrightness < 1 || d.Output.NativeBrightness > 255 {
		return Config{}, errors.New("output.native_brightness must be in [1,255]")
	}
	if d.Safety.BrightnessCeiling < 1 || d.Safety.BrightnessCeiling > math.MaxUint8 {
		return Config{}, errors.New("safety.brightness_ceiling must be in [1,255]")
	}
	safety := render.Safety{RedCurrent: d.Safety.RedCurrent, GreenCurrent: d.Safety.GreenCurrent, BlueCurrent: d.Safety.BlueCurrent, WhiteCurrent: d.Safety.WhiteCurrent, MaxStripCurrent: d.Safety.MaxStripCurrent, BrightnessCeiling: uint8(d.Safety.BrightnessCeiling)}
	// Renderer construction is the public safety validator and also proves the
	// configured strip/safety combination is usable before hardware ownership.
	if _, err := render.New(d.Output.StripLength, safety); err != nil {
		return Config{}, fmt.Errorf("validate safety: %w", err)
	}
	tick, err := parseDuration("service.tick_interval", d.Service.TickInterval, minTickInterval, maxTickInterval)
	if err != nil {
		return Config{}, err
	}
	syncEvery, err := parseDuration("service.synchronization_interval", d.Service.SynchronizationInterval, tick, maxSyncInterval)
	if err != nil {
		return Config{}, err
	}
	cleanup, err := parseDuration("service.cleanup_timeout", d.Service.CleanupTimeout, time.Millisecond, maxCleanup)
	if err != nil {
		return Config{}, err
	}
	return Config{StatePath: d.StatePath, Output: d.Output, Safety: safety, Service: Service{TickInterval: tick, SynchronizationInterval: syncEvery, CleanupTimeout: cleanup}}, nil
}

func parseDuration(name, value string, min, max time.Duration) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if d < min || d > max {
		return 0, fmt.Errorf("%s must be between %s and %s", name, min, max)
	}
	return d, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				name := k.(string)
				if seen[name] {
					return fmt.Errorf("duplicate field %q", name)
				}
				seen[name] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	return walk()
}
