package clock

import "time"

// FixedTimeline is an immutable timeline anchored to one absolute instant.
type FixedTimeline struct{ instant time.Time }

// NewFixedTimeline anchors a timeline to the supplied absolute instant.
func NewFixedTimeline(instant time.Time) FixedTimeline {
	return FixedTimeline{instant: instant}
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
