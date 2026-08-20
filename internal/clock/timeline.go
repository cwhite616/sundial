package clock

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"
)

type TimelineMode string

const (
	TimelineAuto        TimelineMode = "auto"
	TimelineFixed       TimelineMode = "fixed"
	TimelineAccelerated TimelineMode = "accelerated"
)

var (
	ErrInvalidAccelerationRate = errors.New("invalid acceleration rate")
	ErrTimelineRange           = errors.New("timeline result out of range")
)

// Timeline is an immutable, sample-driven source of effective absolute time.
type Timeline struct {
	mode            TimelineMode
	fixed           time.Time
	realAnchor      time.Time
	effectiveAnchor time.Time
	rate            float64
	realReading     time.Duration
}

// Sample keeps wall time and monotonic process time distinct so wall-clock
// corrections cannot influence accelerated elapsed-time calculation.
type Sample struct {
	Wall      time.Time
	Monotonic time.Duration
}

func NewAutoTimeline() Timeline { return Timeline{mode: TimelineAuto} }

func NewAcceleratedTimelineFromSample(realAnchor Sample, effectiveAnchor time.Time, rate float64) (Timeline, error) {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return Timeline{}, fmt.Errorf("create accelerated timeline: %w: %v", ErrInvalidAccelerationRate, rate)
	}
	if realAnchor.Wall.IsZero() || effectiveAnchor.IsZero() {
		return Timeline{}, fmt.Errorf("create accelerated timeline: %w: anchors must be absolute instants", ErrTimelineRange)
	}
	if realAnchor.Monotonic < 0 {
		return Timeline{}, fmt.Errorf("create accelerated timeline: %w: monotonic anchor must not be negative", ErrTimelineRange)
	}
	return Timeline{mode: TimelineAccelerated, realAnchor: realAnchor.Wall, realReading: realAnchor.Monotonic, effectiveAnchor: effectiveAnchor, rate: rate}, nil
}

func (t Timeline) Mode() TimelineMode { return t.mode }
func (t Timeline) Rate() float64      { return t.rate }

func (t Timeline) Validate() error {
	switch t.mode {
	case TimelineAuto, TimelineFixed:
		if t.mode == TimelineFixed && t.fixed.IsZero() {
			return fmt.Errorf("validate fixed timeline: %w: zero instant", ErrTimelineRange)
		}
		return nil
	case TimelineAccelerated:
		if t.rate <= 0 || math.IsNaN(t.rate) || math.IsInf(t.rate, 0) {
			return fmt.Errorf("validate accelerated timeline: %w", ErrInvalidAccelerationRate)
		}
		if t.realAnchor.IsZero() || t.effectiveAnchor.IsZero() || t.realReading < 0 {
			return fmt.Errorf("validate accelerated timeline: %w", ErrTimelineRange)
		}
		return nil
	default:
		return fmt.Errorf("validate timeline: unknown mode %q", t.mode)
	}
}

func (t Timeline) EffectiveTime(sample time.Time) (time.Time, error) {
	reading := time.Duration(0)
	if t.mode == TimelineAccelerated {
		delta := sample.Sub(t.realAnchor)
		if delta == time.Duration(math.MaxInt64) || delta == time.Duration(math.MinInt64) ||
			(delta > 0 && t.realReading > time.Duration(math.MaxInt64)-delta) ||
			(delta < 0 && t.realReading < time.Duration(math.MinInt64)-delta) {
			return time.Time{}, fmt.Errorf("evaluate accelerated compatibility sample: %w", ErrTimelineRange)
		}
		reading = t.realReading + delta
	}
	return t.EffectiveTimeSample(Sample{Wall: sample, Monotonic: reading})
}

func (t Timeline) EffectiveTimeSample(sample Sample) (time.Time, error) {
	switch t.mode {
	case TimelineAuto:
		if sample.Wall.IsZero() {
			return time.Time{}, fmt.Errorf("evaluate auto timeline: %w: zero wall sample", ErrTimelineRange)
		}
		return sample.Wall, nil
	case TimelineFixed:
		if t.fixed.IsZero() {
			return time.Time{}, fmt.Errorf("evaluate fixed timeline: %w: zero instant", ErrTimelineRange)
		}
		return t.fixed, nil
	case TimelineAccelerated:
		if sample.Monotonic < t.realReading {
			return time.Time{}, fmt.Errorf("evaluate accelerated timeline: %w: monotonic reading regressed", ErrTimelineRange)
		}
		elapsed := sample.Monotonic - t.realReading
		if elapsed == time.Duration(math.MaxInt64) || elapsed == time.Duration(math.MinInt64) {
			return time.Time{}, fmt.Errorf("evaluate accelerated timeline elapsed: %w", ErrTimelineRange)
		}
		scaled, ok := scaleDuration(elapsed, t.rate)
		if !ok {
			return time.Time{}, fmt.Errorf("evaluate accelerated timeline: %w", ErrTimelineRange)
		}
		result := t.effectiveAnchor.Add(scaled)
		if result.Sub(t.effectiveAnchor) != scaled {
			return time.Time{}, fmt.Errorf("evaluate accelerated timeline result: %w", ErrTimelineRange)
		}
		return result, nil
	default:
		return time.Time{}, fmt.Errorf("evaluate timeline: unknown mode %q", t.mode)
	}
}

func scaleDuration(elapsed time.Duration, rate float64) (time.Duration, bool) {
	rateValue := new(big.Rat)
	if rateValue.SetFloat64(rate) == nil {
		return 0, false
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(elapsed)), rateValue.Num())
	scaled := new(big.Int).Quo(numerator, rateValue.Denom())
	if !scaled.IsInt64() {
		return 0, false
	}
	value := scaled.Int64()
	if value == math.MaxInt64 || value == math.MinInt64 {
		return 0, false
	}
	return time.Duration(value), true
}

func (t Timeline) EvaluateSample(sample Sample, calibration Calibration, location *time.Location) (time.Time, int, error) {
	effective, err := t.EffectiveTimeSample(sample)
	if err != nil {
		return time.Time{}, 0, err
	}
	position, err := EvaluatePosition(calibration, effective, location)
	if err != nil {
		return effective, 0, fmt.Errorf("evaluate timeline position: %w", err)
	}
	return effective, position, nil
}

func (t Timeline) Evaluate(sample time.Time, calibration Calibration, location *time.Location) (time.Time, int, error) {
	effective, err := t.EffectiveTime(sample)
	if err != nil {
		return time.Time{}, 0, err
	}
	position, err := EvaluatePosition(calibration, effective, location)
	if err != nil {
		return effective, 0, fmt.Errorf("evaluate timeline position: %w", err)
	}
	return effective, position, nil
}

// FixedTimeline is an immutable timeline anchored to one absolute instant.
type FixedTimeline struct{ instant time.Time }

// NewFixedTimeline anchors a timeline to the supplied absolute instant.
func NewFixedTimeline(instant time.Time) FixedTimeline {
	return FixedTimeline{instant: instant}
}

func NewTimelineFixed(instant time.Time) Timeline {
	return Timeline{mode: TimelineFixed, fixed: instant}
}

// NewFixedTimelineFromCivil resolves a civil input uniquely before producing
// a timeline. Invalid, ambiguous, and nonexistent inputs return no candidate.
func NewFixedTimelineFromCivil(civil CivilTime, location *time.Location) (FixedTimeline, error) {
	instant, err := ResolveCivilTime(civil, location)
	if err != nil {
		return FixedTimeline{}, err
	}
	return NewFixedTimeline(instant), nil
}

// EffectiveTime returns the fixed instant. sample is accepted to give timeline
// modes a common evaluation shape, but fixed mode intentionally ignores it.
func (t FixedTimeline) EffectiveTime(sample time.Time) time.Time {
	return t.instant
}

// Evaluate returns both the fixed effective instant and its calibrated pixel.
func (t FixedTimeline) Evaluate(sample time.Time, calibration Calibration, location *time.Location) (time.Time, int, error) {
	effective := t.EffectiveTime(sample)
	position, err := EvaluatePosition(calibration, effective, location)
	if err != nil {
		return effective, 0, err
	}
	return effective, position, nil
}
