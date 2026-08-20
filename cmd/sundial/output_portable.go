//go:build !linux || (!arm && !arm64) || !cgo

package main

import (
	"context"
	"fmt"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/config"
	"github.com/cwhite616/sundial/internal/leds/simulated"
)

const (
	previewOutputDescription = "simulated (no physical LED output)"
	physicalPreviewOutput    = false
)

func newPreviewOutput(context.Context) (app.Output, error) {
	return simulated.New(stripLength)
}

func newDeviceOutput(context.Context, config.Output) (app.Output, error) {
	return nil, fmt.Errorf("initialize physical output: rpi-ws281x is unavailable in this build (requires linux/arm with cgo)")
}
