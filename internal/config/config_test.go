package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const valid = `{
 "state_path":"/var/lib/sundial/state.json",
 "output":{"driver":"rpi-ws281x","strip_length":144,"native_brightness":255},
 "safety":{"red_current":20,"green_current":20,"blue_current":20,"white_current":20,"max_strip_current":127500,"brightness_ceiling":200},
 "service":{"tick_interval":"1s","synchronization_interval":"5m","cleanup_timeout":"1s"}
}`

func TestDecodeValidConfiguration(t *testing.T) {
	c, err := Decode(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if c.Output != (Output{Driver: "rpi-ws281x", StripLength: 144, NativeBrightness: 255}) || c.Service.TickInterval != time.Second || c.Service.SynchronizationInterval != 5*time.Minute || c.Service.CleanupTimeout != time.Second || c.Safety.BrightnessCeiling != 200 {
		t.Fatalf("unexpected config: %+v", c)
	}
}

func TestDecodeRejectsUnsafeAndNonStrictConfiguration(t *testing.T) {
	tests := []struct{ name, old, replacement string }{
		{"unknown", `"state_path"`, `"extra":true,"state_path"`},
		{"duplicate", `"state_path":"/var/lib/sundial/state.json"`, `"state_path":"/a","state_path":"/b"`},
		{"zero safety", `"red_current":20`, `"red_current":0`},
		{"bad brightness", `"native_brightness":255`, `"native_brightness":256`},
		{"unbounded cadence", `"tick_interval":"1s"`, `"tick_interval":"24h"`},
		{"relative state path", `"/var/lib/sundial/state.json"`, `"state.json"`},
		{"invalid driver", `"rpi-ws281x"`, `"simulated"`},
		{"nonpositive strip", `"strip_length":144`, `"strip_length":0`},
		{"strip above maximum", `"strip_length":144`, `"strip_length":513`},
		{"missing output field", `,"native_brightness":255`, ``},
		{"missing service field", `,"cleanup_timeout":"1s"`, ``},
		{"tick too short", `"tick_interval":"1s"`, `"tick_interval":"1ms"`},
		{"sync below tick", `"synchronization_interval":"5m"`, `"synchronization_interval":"500ms"`},
		{"sync too long", `"synchronization_interval":"5m"`, `"synchronization_interval":"25h"`},
		{"cleanup zero", `"cleanup_timeout":"1s"`, `"cleanup_timeout":"0s"`},
		{"cleanup too long", `"cleanup_timeout":"1s"`, `"cleanup_timeout":"31s"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(strings.Replace(valid, tt.old, tt.replacement, 1)))
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDecodeAcceptsConfiguredBoundaries(t *testing.T) {
	document := strings.NewReplacer(
		`"strip_length":144`, `"strip_length":512`,
		`"native_brightness":255`, `"native_brightness":1`,
		`"brightness_ceiling":200`, `"brightness_ceiling":255`,
		`"tick_interval":"1s"`, `"tick_interval":"10ms"`,
		`"synchronization_interval":"5m"`, `"synchronization_interval":"24h"`,
		`"cleanup_timeout":"1s"`, `"cleanup_timeout":"30s"`,
	).Replace(valid)
	if _, err := Decode(strings.NewReader(document)); err != nil {
		t.Fatalf("accepted boundaries rejected: %v", err)
	}
}

func TestLoadReadsFileAndPreservesPathErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Output.NativeBrightness != 255 || got.StatePath != "/var/lib/sundial/state.json" {
		t.Fatalf("loaded config = %+v", got)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
}

func TestDecodeRejectsNilOversizedAndTrailingInput(t *testing.T) {
	var typedNil *bytes.Reader
	for name, reader := range map[string]io.Reader{"nil": nil, "typed nil": typedNil, "oversized": strings.NewReader(strings.Repeat(" ", maxDocumentSize+1)), "trailing": strings.NewReader(valid + ` {}`)} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(reader); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
