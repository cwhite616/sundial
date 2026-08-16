package render

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
)

var ErrInvalidSafety = errors.New("invalid safety configuration")

// Safety is immutable after it is passed to a Renderer. Every channel current
// coefficient is the worst-case current for one channel step (value 1 of 255),
// and MaxStripCurrent must use that same integer unit. BrightnessCeiling is a
// global uniform scale ceiling: 255 permits 100% and 128 permits at most
// 128/255 of every channel. It is not a per-channel clamp.
type Safety struct {
	RedCurrent        uint64
	GreenCurrent      uint64
	BlueCurrent       uint64
	WhiteCurrent      uint64
	MaxStripCurrent   uint64
	BrightnessCeiling uint8
}

func (s Safety) validate() error {
	if s.RedCurrent == 0 || s.GreenCurrent == 0 || s.BlueCurrent == 0 || s.WhiteCurrent == 0 {
		return fmt.Errorf("%w: every channel current coefficient must be positive", ErrInvalidSafety)
	}
	if s.MaxStripCurrent == 0 {
		return fmt.Errorf("%w: strip-current budget must be positive", ErrInvalidSafety)
	}
	if s.BrightnessCeiling == 0 {
		return fmt.Errorf("%w: brightness ceiling must be positive", ErrInvalidSafety)
	}
	return nil
}

func channelCurrent(value uint8, coefficient uint64) (uint64, error) {
	if value == 0 {
		return 0, nil
	}
	if coefficient > math.MaxUint64/uint64(value) {
		return 0, fmt.Errorf("%w: channel current calculation overflows", ErrInvalidSafety)
	}
	return uint64(value) * coefficient, nil
}

func estimateCurrent(pixels []Pixel, s Safety) (uint64, error) {
	var total uint64
	for _, p := range pixels {
		parts := [...]struct {
			value       uint8
			coefficient uint64
		}{{p.R, s.RedCurrent}, {p.G, s.GreenCurrent}, {p.B, s.BlueCurrent}, {p.W, s.WhiteCurrent}}
		for _, part := range parts {
			current, err := channelCurrent(part.value, part.coefficient)
			if err != nil || math.MaxUint64-total < current {
				return 0, fmt.Errorf("%w: aggregate current calculation overflows", ErrInvalidSafety)
			}
			total += current
		}
	}
	return total, nil
}

// applySafety uses a single rational scale and floors every channel. Flooring
// makes quantization conservative: the configured ceilings cannot be crossed.
func applySafety(pixels []Pixel, s Safety) ([]Pixel, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	current, err := estimateCurrent(pixels, s)
	if err != nil {
		return nil, err
	}
	numerator, denominator := uint64(s.BrightnessCeiling), uint64(255)
	budgetHigh, budgetLow := bits.Mul64(s.MaxStripCurrent, denominator)
	currentHigh, currentLow := bits.Mul64(current, numerator)
	if current > 0 && (budgetHigh < currentHigh || (budgetHigh == currentHigh && budgetLow < currentLow)) {
		numerator, denominator = s.MaxStripCurrent, current
	}
	result := make([]Pixel, len(pixels))
	for i, p := range pixels {
		result[i] = Pixel{
			R: scaleChannel(p.R, numerator, denominator),
			G: scaleChannel(p.G, numerator, denominator),
			B: scaleChannel(p.B, numerator, denominator),
			W: scaleChannel(p.W, numerator, denominator),
		}
	}
	return result, nil
}

func scaleChannel(value uint8, numerator, denominator uint64) uint8 {
	high, low := bits.Mul64(uint64(value), numerator)
	quotient, _ := bits.Div64(high, low, denominator)
	return uint8(quotient)
}
