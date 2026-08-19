package clock

import (
	"errors"
	"testing"
	"time"
)

func TestFixedTimelineIsRepeatable(t *testing.T) {
	anchor := time.Date(2026, 8, 19, 12, 30, 0, 123, time.UTC)
	timeline := NewFixedTimeline(anchor)
	calibration, err := NewCalibration(10, []CalibrationPoint{
		{Time: mustTOD(t, 0, 0), Pixel: 0},
		{Time: mustTOD(t, 12, 0), Pixel: 6},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, sample := range []time.Time{time.Time{}, anchor.Add(-100 * time.Hour), anchor.Add(100 * time.Hour)} {
		effective, position, err := timeline.Evaluate(sample, calibration, time.UTC)
		if err != nil || !effective.Equal(anchor) || effective.Nanosecond() != anchor.Nanosecond() || position != 6 {
			t.Fatalf("Evaluate() = %v, %d, %v; want anchor, 6, nil", effective, position, err)
		}
	}
}

func TestFixedTimelineEvaluatePreservesAnchorOnFailure(t *testing.T) {
	anchor := time.Date(2026, 8, 19, 12, 30, 0, 123, time.UTC)
	effective, position, err := NewFixedTimeline(anchor).Evaluate(time.Time{}, Calibration{}, time.UTC)
	if !effective.Equal(anchor) || position != 0 || !errors.Is(err, ErrInvalidStripLength) {
		t.Fatalf("Evaluate() = %v, %d, %v; want anchor, 0, ErrInvalidStripLength", effective, position, err)
	}
}

func TestNewFixedTimelineFromCivil(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	if err != nil {
		t.Fatal(err)
	}
	valid := CivilTime{Year: 2026, Month: 8, Day: 19, Hour: 9}
	timeline, err := NewFixedTimelineFromCivil(valid, detroit)
	if err != nil {
		t.Fatal(err)
	}
	if got := timeline.EffectiveTime(time.Time{}); got.Hour() != 9 || got.Location() != detroit {
		t.Fatalf("resolved effective time = %v", got)
	}

	tests := []struct {
		name  string
		civil CivilTime
		zone  *time.Location
		want  error
	}{
		{"invalid", CivilTime{Year: 2026, Month: 2, Day: 30}, detroit, ErrInvalidCivilTime},
		{"nonexistent", CivilTime{Year: 2026, Month: 3, Day: 8, Hour: 2, Minute: 30}, detroit, ErrNonexistentCivilTime},
		{"ambiguous", CivilTime{Year: 2026, Month: 11, Day: 1, Hour: 1, Minute: 30}, detroit, ErrAmbiguousCivilTime},
		{"nil location", valid, nil, ErrInvalidCivilTime},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewFixedTimelineFromCivil(test.civil, test.zone)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
			if got != (FixedTimeline{}) {
				t.Fatalf("timeline = %#v; want zero candidate", got)
			}
		})
	}
}
