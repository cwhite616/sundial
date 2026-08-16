package app

import (
	"context"

	"github.com/cwhite616/sundial/internal/render"
)

// Output is owned by the application consumer. Implementations must snapshot
// frames before returning from WriteFrame if they retain them. WriteFrame,
// Clear, and Close must return promptly when their context is canceled.
type Output interface {
	WriteFrame(context.Context, render.Frame) error
	Clear(context.Context) error
	Close(context.Context) error
}
