//go:build linux && (arm || arm64) && cgo

package main

import (
	"context"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/config"
	"github.com/cwhite616/sundial/internal/leds/rpiws281x"
)

const (
	previewOutputDescription = "physical rpi-ws281x (GPIO18, SK6812 GRBW, native brightness 255)"
	physicalPreviewOutput    = true
)

func newPreviewOutput(ctx context.Context) (app.Output, error) {
	return rpiws281x.New(ctx, stripLength, nativeDriverBrightness)
}

func newDeviceOutput(ctx context.Context, settings config.Output) (app.Output, error) {
	return rpiws281x.New(ctx, settings.StripLength, settings.NativeBrightness)
}
