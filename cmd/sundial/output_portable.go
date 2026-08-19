//go:build !linux || !arm || !cgo

package main

import (
	"context"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/leds/simulated"
)

func newPreviewOutput(context.Context) (app.Output, error) {
	return simulated.New(stripLength)
}
