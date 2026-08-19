//go:build !linux || (!arm && !arm64) || !cgo

package main

import (
	"context"
	"testing"

	"github.com/cwhite616/sundial/internal/leds/simulated"
)

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
}
