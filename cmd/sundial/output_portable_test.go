//go:build !linux || (!arm && !arm64) || !cgo

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/cwhite616/sundial/internal/config"
	"github.com/cwhite616/sundial/internal/leds/simulated"
)

func TestPortableServiceOutputCannotMasqueradeAsPhysical(t *testing.T) {
	_, err := newDeviceOutput(context.Background(), config.Output{Driver: "rpi-ws281x", StripLength: 144, NativeBrightness: 255})
	if err == nil || !strings.Contains(err.Error(), "unavailable in this build") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPortableCompositionUsesExactLengthSimulator(t *testing.T) {
	output, err := newPreviewOutput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	driver, ok := output.(*simulated.Driver)
	if !ok {
		t.Fatalf("portable output type = %T", output)
	}
	if got := driver.Snapshot().Len(); got != stripLength {
		t.Fatalf("strip length = %d, want %d", got, stripLength)
	}
	if physicalPreviewOutput || previewOutputDescription != "simulated (no physical LED output)" {
		t.Fatalf("portable identity: physical=%v description=%q", physicalPreviewOutput, previewOutputDescription)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := selectedDiagnosticHold()(canceled); err != nil {
		t.Fatalf("simulator applied physical diagnostic hold: %v", err)
	}
}
