package clock

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrInvalidStripLength       = errors.New("invalid strip length")
	ErrTooFewCalibrationPoints  = errors.New("too few calibration points")
	ErrDuplicateCalibrationTime = errors.New("duplicate calibration time")
	ErrPixelOutOfRange          = errors.New("calibration pixel out of range")
)

// CalibrationPoint maps one local time of day directly to a physical pixel.
type CalibrationPoint struct {
	Time  TimeOfDay
	Pixel int
}

// Calibration is an immutable, ascending, repeating daily mapping.
type Calibration struct {
	points      []CalibrationPoint
	stripLength int
}

func NewCalibration(stripLength int, points []CalibrationPoint) (Calibration, error) {
	if stripLength <= 0 {
		return Calibration{}, fmt.Errorf("%w: %d", ErrInvalidStripLength, stripLength)
	}
	if len(points) < 2 {
		return Calibration{}, fmt.Errorf("%w: got %d, need at least 2", ErrTooFewCalibrationPoints, len(points))
	}
	owned := append([]CalibrationPoint(nil), points...)
	for i, point := range owned {
		if point.Time.nanoseconds < 0 || point.Time.nanoseconds >= int64(24*60*60*1e9) {
			return Calibration{}, fmt.Errorf("point %d: %w", i, ErrInvalidTimeOfDay)
		}
		if point.Pixel < 0 || point.Pixel >= stripLength {
			return Calibration{}, fmt.Errorf("point %d: %w: %d outside [0, %d)", i, ErrPixelOutOfRange, point.Pixel, stripLength)
		}
	}
	sort.SliceStable(owned, func(i, j int) bool { return owned[i].Time.nanoseconds < owned[j].Time.nanoseconds })
	for i := 1; i < len(owned); i++ {
		if owned[i-1].Time == owned[i].Time {
			return Calibration{}, fmt.Errorf("%w: %s", ErrDuplicateCalibrationTime, owned[i].Time.Duration())
		}
	}
	return Calibration{points: owned, stripLength: stripLength}, nil
}

func (c Calibration) StripLength() int           { return c.stripLength }
func (c Calibration) Points() []CalibrationPoint { return append([]CalibrationPoint(nil), c.points...) }

// CyclicPairs returns each point and its successor. The last pair wraps once
// across midnight to the first point; no interpolation policy is implied.
func (c Calibration) CyclicPairs() []struct {
	From, To        CalibrationPoint
	CrossesMidnight bool
} {
	result := make([]struct {
		From, To        CalibrationPoint
		CrossesMidnight bool
	}, len(c.points))
	for i := range c.points {
		result[i].From = c.points[i]
		result[i].To = c.points[(i+1)%len(c.points)]
		result[i].CrossesMidnight = i == len(c.points)-1
	}
	return result
}
