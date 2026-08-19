//go:build !linux || (!arm && !arm64) || !cgo

package main

import (
	"context"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/leds/simulated"
)

const (
	previewOutputDescription = "simulated (no physical LED output)"
	physicalPreviewOutput    = false
)

func newPreviewOutput(context.Context) (app.Output, error) {
	return simulated.New(stripLength)
}
