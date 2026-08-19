//go:build linux && arm && cgo

package main

import (
	"context"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/leds/rpiws281x"
)

func newPreviewOutput(ctx context.Context) (app.Output, error) {
	return rpiws281x.New(ctx, stripLength, driverBrightness)
}
