package clock

import (
	"errors"
	"testing"
)

func mustTOD(t *testing.T, hour, minute int) TimeOfDay {
	t.Helper()
	value, err := NewTimeOfDay(hour, minute, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCalibrationNormalizesOwnsAndWraps(t *testing.T) {
	input := []CalibrationPoint{{mustTOD(t, 23, 0), 9}, {mustTOD(t, 0, 15), 0}, {mustTOD(t, 12, 0), 5}}
	calibration, err := NewCalibration(10, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Pixel = 1
	points := calibration.Points()
	if points[0].Pixel != 0 || points[1].Pixel != 5 || points[2].Pixel != 9 {
		t.Fatalf("points = %#v", points)
	}
	points[0].Pixel = 8
	if calibration.Points()[0].Pixel != 0 {
		t.Fatal("accessor exposed mutable storage")
	}
	pairs := calibration.CyclicPairs()
	if len(pairs) != 3 || pairs[0].CrossesMidnight || pairs[1].CrossesMidnight || !pairs[2].CrossesMidnight || pairs[2].To != calibration.Points()[0] {
		t.Fatalf("cyclic pairs = %#v", pairs)
	}
}

func TestCalibrationRejectsInvalidInputs(t *testing.T) {
	a := mustTOD(t, 1, 0)
	b := mustTOD(t, 2, 0)
	tests := []struct {
		name   string
		length int
		points []CalibrationPoint
		want   error
	}{
		{"strip", 0, []CalibrationPoint{{a, 0}, {b, 1}}, ErrInvalidStripLength},
		{"count", 2, []CalibrationPoint{{a, 0}}, ErrTooFewCalibrationPoints},
		{"duplicate", 2, []CalibrationPoint{{a, 0}, {a, 1}}, ErrDuplicateCalibrationTime},
		{"negative pixel", 2, []CalibrationPoint{{a, -1}, {b, 1}}, ErrPixelOutOfRange},
		{"pixel equal length", 2, []CalibrationPoint{{a, 0}, {b, 2}}, ErrPixelOutOfRange},
		{"time exactly 24h", 2, []CalibrationPoint{{a, 0}, {Time: TimeOfDay{nanoseconds: int64(24 * 60 * 60 * 1e9)}, Pixel: 1}}, ErrInvalidTimeOfDay},
		{"negative time", 2, []CalibrationPoint{{a, 0}, {Time: TimeOfDay{nanoseconds: -1}, Pixel: 1}}, ErrInvalidTimeOfDay},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewCalibration(test.length, test.points)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if got.StripLength() != 0 || got.Points() != nil {
				t.Fatalf("partial calibration = %#v", got)
			}
		})
	}
}

func TestCalibrationAcceptsExactlyTwoPointsAtStripEdges(t *testing.T) {
	late, early := mustTOD(t, 23, 45), mustTOD(t, 0, 15)
	calibration, err := NewCalibration(10, []CalibrationPoint{{late, 9}, {early, 0}})
	if err != nil {
		t.Fatal(err)
	}
	points := calibration.Points()
	if len(points) != 2 || points[0] != (CalibrationPoint{early, 0}) || points[1] != (CalibrationPoint{late, 9}) {
		t.Fatalf("points = %#v", points)
	}
	pairs := calibration.CyclicPairs()
	if len(pairs) != 2 || pairs[0].CrossesMidnight || !pairs[1].CrossesMidnight || pairs[1].From != points[1] || pairs[1].To != points[0] {
		t.Fatalf("pairs = %#v", pairs)
	}
}
