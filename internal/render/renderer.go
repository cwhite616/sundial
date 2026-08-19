package render

import "fmt"

type ArtificialSun struct {
	Position         int
	Color            Pixel
	IntensityProfile []uint8
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
	profile := sun.IntensityProfile
	if len(profile) == 0 {
		profile = []uint8{255}
	}
	if len(profile)%2 == 0 {
		return dark, fmt.Errorf("render artificial sun: intensity profile length must be odd: %d", len(profile))
	}
	pixels := dark.Pixels()
	center := len(profile) / 2
	for offset, intensity := range profile {
		position := sun.Position + offset - center
		if position < 0 || position >= r.stripLength {
			continue
		}
		pixels[position] = scalePixel(sun.Color, intensity)
	}
	safe, err := applySafety(pixels, r.safety)
	if err != nil {
		return dark, fmt.Errorf("render artificial sun safely: %w", err)
	}
	return NewFrame(safe), nil
}

func scalePixel(color Pixel, intensity uint8) Pixel {
	return Pixel{
		R: uint8(uint16(color.R) * uint16(intensity) / 255),
		G: uint8(uint16(color.G) * uint16(intensity) / 255),
		B: uint8(uint16(color.B) * uint16(intensity) / 255),
		W: uint8(uint16(color.W) * uint16(intensity) / 255),
	}
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
