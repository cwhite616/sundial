package render

import "fmt"

type ArtificialSun struct {
	Position int
	Color    Pixel
}

type Renderer struct {
	stripLength int
	safety      Safety
}

func New(stripLength int, safety Safety) (*Renderer, error) {
	if stripLength <= 0 {
		return nil, fmt.Errorf("initialize renderer: strip length must be positive: %d", stripLength)
	}
	if err := safety.validate(); err != nil {
		return nil, fmt.Errorf("initialize renderer: %w", err)
	}
	return &Renderer{stripLength: stripLength, safety: safety}, nil
}

// Render creates the only pixel representation of semantic Artificial Sun input.
func (r *Renderer) Render(sun ArtificialSun) (Frame, error) {
	dark, _ := DarkFrame(r.stripLength)
	if err := r.safety.validate(); err != nil {
		return dark, fmt.Errorf("render artificial sun: %w", err)
	}
	if sun.Position < 0 || sun.Position >= r.stripLength {
		return dark, fmt.Errorf("render artificial sun: position %d outside [0, %d)", sun.Position, r.stripLength)
	}
	pixels := dark.Pixels()
	pixels[sun.Position] = sun.Color
	safe, err := applySafety(pixels, r.safety)
	if err != nil {
		return dark, fmt.Errorf("render artificial sun safely: %w", err)
	}
	return NewFrame(safe), nil
}

// Render permits fail-dark inspection if startup validation fails.
func Render(stripLength int, safety Safety, sun ArtificialSun) (Frame, error) {
	dark, darkErr := DarkFrame(stripLength)
	if darkErr != nil {
		return Frame{}, fmt.Errorf("render artificial sun: %w", darkErr)
	}
	r, err := New(stripLength, safety)
	if err != nil {
		return dark, err
	}
	return r.Render(sun)
}
