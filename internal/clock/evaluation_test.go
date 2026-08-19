package clock

import (
	"errors"
	"testing"
	"time"
)

func TestEvaluateTimeOfDay(t *testing.T) {
	calibration, err := NewCalibration(11, []CalibrationPoint{
		{Time: mustTOD(t, 6, 0), Pixel: 2},
		{Time: mustTOD(t, 12, 0), Pixel: 8},
		{Time: mustTOD(t, 18, 0), Pixel: 8},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		time TimeOfDay
		want int
	}{
		{"exact", mustTOD(t, 6, 0), 2},
		{"ascending", mustTOD(t, 9, 0), 5},
		{"repeated pixel", mustTOD(t, 15, 0), 8},
		{"wrap before first", mustTOD(t, 0, 0), 5},
		{"wrap after last", mustTOD(t, 21, 0), 7},
		{"half tie upward ascending", mustTOD(t, 6, 30), 3},
		{"half tie upward descending", mustTOD(t, 19, 0), 8},
		{"exact final point", mustTOD(t, 18, 0), 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EvaluateTimeOfDay(calibration, test.time)
			if err != nil || got != test.want {
				t.Fatalf("EvaluateTimeOfDay() = %d, %v; want %d, nil", got, err, test.want)
			}
		})
	}
}

func TestEvaluateTimeOfDayDescendingInterval(t *testing.T) {
	calibration, err := NewCalibration(11, []CalibrationPoint{
		{Time: mustTOD(t, 6, 0), Pixel: 9},
		{Time: mustTOD(t, 10, 0), Pixel: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := EvaluateTimeOfDay(calibration, mustTOD(t, 8, 0))
	if err != nil || got != 6 {
		t.Fatalf("EvaluateTimeOfDay() = %d, %v; want 6, nil", got, err)
	}
}

func TestEvaluateTimeOfDayLargePixelIndicesDoNotOverflow(t *testing.T) {
	maximumInt := int(^uint(0) >> 1)
	calibration, err := NewCalibration(maximumInt, []CalibrationPoint{
		{Time: mustTOD(t, 0, 0), Pixel: maximumInt - 2},
		{Time: mustTOD(t, 2, 0), Pixel: maximumInt - 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := EvaluateTimeOfDay(calibration, mustTOD(t, 1, 0))
	if err != nil || got != maximumInt-1 {
		t.Fatalf("EvaluateTimeOfDay() = %d, %v; want %d, nil", got, err, maximumInt-1)
	}
}

func TestEvaluateTimeOfDayRejectsUnusableInputs(t *testing.T) {
	validTime := mustTOD(t, 0, 0)
	tests := []struct {
		name        string
		calibration Calibration
		value       TimeOfDay
		want        error
	}{
		{"zero calibration", Calibration{}, validTime, ErrInvalidStripLength},
		{"too few points", Calibration{stripLength: 2, points: []CalibrationPoint{{Time: validTime, Pixel: 0}}}, validTime, ErrTooFewCalibrationPoints},
		{"invalid time", Calibration{stripLength: 2, points: []CalibrationPoint{{Time: validTime, Pixel: 0}, {Time: mustTOD(t, 1, 0), Pixel: 1}}}, TimeOfDay{nanoseconds: dayNanoseconds}, ErrInvalidTimeOfDay},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := EvaluateTimeOfDay(test.calibration, test.value)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
		})
	}
}

func TestEvaluatePositionUsesExplicitLocation(t *testing.T) {
	calibration, err := NewCalibration(10, []CalibrationPoint{
		{Time: mustTOD(t, 0, 0), Pixel: 0},
		{Time: mustTOD(t, 12, 0), Pixel: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	detroit, err := time.LoadLocation("America/Detroit")
	if err != nil {
		t.Fatal(err)
	}
	got, err := EvaluatePosition(calibration, instant, detroit)
	if err != nil || got != 0 {
		t.Fatalf("EvaluatePosition() = %d, %v; want 0, nil", got, err)
	}
	if _, err := EvaluatePosition(calibration, instant, nil); !errors.Is(err, ErrInvalidLocation) {
		t.Fatalf("nil location error = %v; want ErrInvalidLocation", err)
	}
}
