package app

import (
	"context"

	"github.com/cwhite616/sundial/internal/render"
)

// Output is owned by the application consumer. Implementations must snapshot
// frames before returning from WriteFrame if they retain them.
type Output interface {
	WriteFrame(context.Context, render.Frame) error
	Clear(context.Context) error
	Close() error
}
