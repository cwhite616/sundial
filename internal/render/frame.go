package render

import "fmt"

// Pixel is one explicit 8-bit RGBW output value.
type Pixel struct {
	R uint8
	G uint8
	B uint8
	W uint8
}

// Frame is an immutable strip snapshot. Its backing storage is never exposed.
type Frame struct {
	pixels []Pixel
}

// NewFrame takes a defensive copy of pixels.
func NewFrame(pixels []Pixel) Frame {
	return Frame{pixels: append([]Pixel(nil), pixels...)}
}

// DarkFrame creates an exact-length de-energized frame.
func DarkFrame(length int) (Frame, error) {
	if length <= 0 {
		return Frame{}, fmt.Errorf("create dark frame: strip length must be positive: %d", length)
	}
	return NewFrame(make([]Pixel, length)), nil
}

func (f Frame) Len() int { return len(f.pixels) }

// Pixels returns a defensive snapshot.
func (f Frame) Pixels() []Pixel { return append([]Pixel(nil), f.pixels...) }

func (f Frame) Pixel(position int) (Pixel, error) {
	if position < 0 || position >= len(f.pixels) {
		return Pixel{}, fmt.Errorf("read frame position %d: outside [0, %d)", position, len(f.pixels))
	}
	return f.pixels[position], nil
}
