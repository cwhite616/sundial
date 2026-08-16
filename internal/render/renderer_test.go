package render

import (
	"errors"
	"math"
	"testing"
)

func validSafety() Safety {
	return Safety{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 1_000_000, BrightnessCeiling: 255}
}

func TestRenderBoundaryPositionsAndRGBWOrder(t *testing.T) {
	r, err := New(3, validSafety())
	if err != nil {
		t.Fatal(err)
	}
	for _, position := range []int{0, 2} {
		color := Pixel{R: 1, G: 2, B: 3, W: 4}
		frame, err := r.Render(ArtificialSun{Position: position, Color: color})
		if err != nil {
			t.Fatal(err)
		}
		if frame.Len() != 3 {
			t.Fatalf("length = %d", frame.Len())
		}
		for i, pixel := range frame.Pixels() {
			want := Pixel{}
			if i == position {
				want = color
			}
			if pixel != want {
				t.Fatalf("pixel %d = %+v, want %+v", i, pixel, want)
			}
		}
	}
}

func TestFrameDoesNotAliasInputOrOutput(t *testing.T) {
	input := []Pixel{{R: 7}}
	frame := NewFrame(input)
	input[0].R = 8
	output := frame.Pixels()
	output[0].R = 9
	pixel, _ := frame.Pixel(0)
	if pixel.R != 7 {
		t.Fatalf("immutable pixel R = %d", pixel.R)
	}
}

func TestRenderInvalidPositionFailsDark(t *testing.T) {
	r, _ := New(2, validSafety())
	for _, position := range []int{-1, 2} {
		frame, err := r.Render(ArtificialSun{Position: position, Color: Pixel{R: 255}})
		if err == nil {
			t.Fatalf("position %d: expected error", position)
		}
		if got := frame.Pixels(); got[0] != (Pixel{}) || got[1] != (Pixel{}) {
			t.Fatalf("position %d: not dark: %+v", position, got)
		}
	}
}

func TestSafetyUniformlyAppliesBrightnessAndCurrentCeilings(t *testing.T) {
	tests := []struct {
		name   string
		safety Safety
		want   Pixel
	}{
		{"brightness", Safety{1, 1, 1, 1, 9999, 128}, Pixel{128, 64, 32, 16}},
		{"current", Safety{1, 1, 1, 1, 200, 255}, Pixel{50, 50, 50, 50}},
		{"combined chooses tighter", Safety{2, 2, 2, 2, 100, 200}, Pixel{12, 12, 12, 12}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, err := New(1, test.safety)
			if err != nil {
				t.Fatal(err)
			}
			color := Pixel{255, 128, 64, 32}
			if test.name != "brightness" {
				color = Pixel{255, 255, 255, 255}
			}
			frame, err := r.Render(ArtificialSun{0, color})
			if err != nil {
				t.Fatal(err)
			}
			pixel, _ := frame.Pixel(0)
			if pixel != test.want {
				t.Fatalf("pixel = %+v, want %+v", pixel, test.want)
			}
			current, err := estimateCurrent(frame.Pixels(), test.safety)
			if err != nil || current > test.safety.MaxStripCurrent {
				t.Fatalf("current = %d, err = %v", current, err)
			}
		})
	}
}

func TestSafetyRoundingIsConservativeAndUniform(t *testing.T) {
	s := Safety{1, 1, 1, 1, 3, 255}
	frame, err := Render(1, s, ArtificialSun{0, Pixel{2, 1, 0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := frame.Pixel(0)
	if p != (Pixel{2, 1, 0, 0}) {
		t.Fatalf("unexpected no-scale result: %+v", p)
	}
	s.MaxStripCurrent = 2
	frame, err = Render(1, s, ArtificialSun{0, Pixel{2, 1, 0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ = frame.Pixel(0)
	if p != (Pixel{1, 0, 0, 0}) {
		t.Fatalf("floored result = %+v", p)
	}
}

func TestSafetyScalingHandlesLargeIntegerUnitsWithoutOverflow(t *testing.T) {
	coefficient := uint64(math.MaxUint64 / 255)
	s := Safety{RedCurrent: coefficient, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: math.MaxUint64 - 1, BrightnessCeiling: 255}
	frame, err := Render(1, s, ArtificialSun{0, Pixel{R: 255}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := estimateCurrent(frame.Pixels(), s)
	if err != nil || current > s.MaxStripCurrent {
		t.Fatalf("current = %d, budget = %d, err = %v", current, s.MaxStripCurrent, err)
	}
}

func TestEvenChannelValueUsesItsActualOverflowBoundary(t *testing.T) {
	coefficient := uint64(math.MaxUint64 / 2)
	current, err := channelCurrent(2, coefficient)
	if err != nil {
		t.Fatal(err)
	}
	if current != coefficient*2 {
		t.Fatalf("current = %d, want %d", current, coefficient*2)
	}
}

func TestInvalidSafetyReturnsExactDarkFrame(t *testing.T) {
	invalid := []Safety{
		{},
		{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 0, MaxStripCurrent: 10, BrightnessCeiling: 1},
		{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 0, BrightnessCeiling: 1},
		{RedCurrent: 1, GreenCurrent: 1, BlueCurrent: 1, WhiteCurrent: 1, MaxStripCurrent: 10, BrightnessCeiling: 0},
	}
	for _, safety := range invalid {
		frame, err := Render(2, safety, ArtificialSun{0, Pixel{R: 255}})
		if !errors.Is(err, ErrInvalidSafety) {
			t.Fatalf("error = %v", err)
		}
		if frame.Len() != 2 || frame.Pixels()[0] != (Pixel{}) || frame.Pixels()[1] != (Pixel{}) {
			t.Fatalf("frame = %+v", frame.Pixels())
		}
	}
	frame, err := Render(0, validSafety(), ArtificialSun{})
	if err == nil || frame.Len() != 0 {
		t.Fatalf("invalid length frame = %+v, err = %v", frame, err)
	}
}
