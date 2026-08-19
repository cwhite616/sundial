//go:build linux && (arm || arm64) && cgo

package main

import "testing"

func TestNativeCompositionIdentifiesPhysicalOutput(t *testing.T) {
	if !physicalPreviewOutput {
		t.Fatal("native composition did not identify physical output")
	}
	if previewOutputDescription != "physical rpi-ws281x (GPIO18, SK6812 GRBW, native brightness 255)" {
		t.Fatalf("native output description = %q", previewOutputDescription)
	}
	if nativeDriverBrightness != 255 || rendererBrightnessCeiling != 96 {
		t.Fatalf("brightness controls: native=%d renderer=%d", nativeDriverBrightness, rendererBrightnessCeiling)
	}
}
