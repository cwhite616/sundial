package clock

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestAutoTimelineUsesEverySuppliedSample(t *testing.T) {
	timeline := NewAutoTimeline()
	for _, sample := range []time.Time{time.Date(2026, 1, 1, 1, 2, 3, 4, time.UTC), time.Date(2030, 2, 3, 4, 5, 6, 7, time.UTC)} {
		got, err := timeline.EffectiveTime(sample)
		if err != nil || got != sample {
			t.Fatalf("EffectiveTime() = %v, %v; want %v", got, err, sample)
		}
	}
}

func TestAcceleratedTimelineUsesMonotonicElapsedAndRejectsInvalidRates(t *testing.T) {
	realAnchor := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	effectiveAnchor := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	timeline, err := NewAcceleratedTimelineFromSample(Sample{Wall: realAnchor, Monotonic: 10 * time.Second}, effectiveAnchor, 60)
	if err != nil {
		t.Fatal(err)
	}
	// The wall reading jumps backward by hours, while the monotonic reading
	// advances two seconds. Only that monotonic elapsed time is accelerated.
	got, err := timeline.EffectiveTimeSample(Sample{Wall: realAnchor.Add(-4 * time.Hour), Monotonic: 12 * time.Second})
	if err != nil || !got.Equal(effectiveAnchor.Add(2*time.Minute)) {
		t.Fatalf("effective = %v, %v", got, err)
	}
	for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := NewAcceleratedTimelineFromSample(Sample{Wall: realAnchor}, effectiveAnchor, rate); !errors.Is(err, ErrInvalidAccelerationRate) {
			t.Fatalf("rate %v error = %v", rate, err)
		}
	}
}

func TestAcceleratedTimelineRejectsDurationOverflow(t *testing.T) {
	anchor := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	timeline, err := NewAcceleratedTimelineFromSample(Sample{Wall: anchor}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), math.MaxFloat64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.EffectiveTime(anchor.Add(time.Second)); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("error = %v", err)
	}
}

func TestTimelineRejectsZeroAndRegressingSamplesAndSubtractionOverflow(t *testing.T) {
	if err := NewTimelineFixed(time.Time{}).Validate(); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("zero fixed error = %v", err)
	}
	if _, err := NewAutoTimeline().EffectiveTimeSample(Sample{}); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("zero auto error = %v", err)
	}
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	timeline, err := NewAcceleratedTimelineFromSample(Sample{Wall: anchor, Monotonic: 5}, anchor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.EffectiveTimeSample(Sample{Wall: anchor, Monotonic: 4}); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("regression error = %v", err)
	}
}

func TestAcceleratedCompatibilityEvaluationDoesNotSubtractReadingTwice(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	timeline, err := NewAcceleratedTimelineFromSample(Sample{Wall: anchor, Monotonic: 10 * time.Second}, anchor, 2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := timeline.EffectiveTime(anchor.Add(3 * time.Second))
	if err != nil || !got.Equal(anchor.Add(6*time.Second)) {
		t.Fatalf("effective = %v, %v", got, err)
	}
}

func TestAcceleratedRejectsFloatBoundaryThatWouldWrapDuration(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	timeline, err := NewAcceleratedTimelineFromSample(Sample{Wall: anchor}, anchor, float64(math.MaxInt64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.EffectiveTimeSample(Sample{Wall: anchor, Monotonic: 1}); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("boundary error = %v", err)
	}
}

func TestAcceleratedTimelineRejectsNegativeMonotonicAnchor(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NewAcceleratedTimelineFromSample(Sample{Wall: anchor, Monotonic: -1}, anchor, 1); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("negative monotonic anchor error = %v", err)
	}
	invalid := Timeline{mode: TimelineAccelerated, realAnchor: anchor, effectiveAnchor: anchor, rate: 1, realReading: -1}
	if err := invalid.Validate(); !errors.Is(err, ErrTimelineRange) {
		t.Fatalf("negative monotonic validation error = %v", err)
	}
}

func TestAcceleratedTimelineClassifiesInvalidAnchorsAsRangeErrors(t *testing.T) {
	invalid := Timeline{mode: TimelineAccelerated, rate: 1}
	if err := invalid.Validate(); !errors.Is(err, ErrTimelineRange) || errors.Is(err, ErrInvalidAccelerationRate) {
		t.Fatalf("invalid anchor validation error = %v", err)
	}
}

func TestAcceleratedTimelinePreservesNanosecondsBeyondFloatPrecision(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	elapsed := time.Duration(1<<53 + 1)
	timeline, err := NewAcceleratedTimelineFromSample(Sample{Wall: anchor}, anchor, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := timeline.EffectiveTimeSample(Sample{Monotonic: elapsed})
	if err != nil || !got.Equal(anchor.Add(elapsed)) {
		t.Fatalf("effective = %v, %v; want %v", got, err, anchor.Add(elapsed))
	}
}

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
