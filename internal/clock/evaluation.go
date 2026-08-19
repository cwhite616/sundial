package clock

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

var ErrInvalidLocation = errors.New("invalid location")

const dayNanoseconds = int64(24 * time.Hour)

// EvaluateTimeOfDay returns the calibrated physical pixel for a local time of
// day. Between calibration points it provisionally interpolates numeric pixel
// indices linearly and rounds to nearest, with exact half-pixel ties upward.
// The final-to-first interval is treated as one continuous interval across
// midnight; pixel indices themselves are not treated as circular.
func EvaluateTimeOfDay(calibration Calibration, value TimeOfDay) (int, error) {
	if err := validateEvaluationInput(calibration, value); err != nil {
		return 0, err
	}

	points := calibration.points
	query := value.nanoseconds
	for _, point := range points {
		if query == point.Time.nanoseconds {
			return checkedPixel(point.Pixel, calibration.stripLength)
		}
	}

	for i := 0; i < len(points)-1; i++ {
		if query > points[i].Time.nanoseconds && query < points[i+1].Time.nanoseconds {
			return interpolatePixel(points[i], points[i+1], query, calibration.stripLength)
		}
	}

	from := points[len(points)-1]
	to := points[0]
	if query < to.Time.nanoseconds {
		query += dayNanoseconds
	}
	to.Time.nanoseconds += dayNanoseconds
	return interpolatePixel(from, to, query, calibration.stripLength)
}

// EvaluatePosition converts effective through location, then evaluates its
// local clock fields. The location must be supplied explicitly.
func EvaluatePosition(calibration Calibration, effective time.Time, location *time.Location) (int, error) {
	if location == nil {
		return 0, fmt.Errorf("%w: nil location", ErrInvalidLocation)
	}
	return EvaluateTimeOfDay(calibration, TimeOfDayFromTime(effective.In(location)))
}

func validateEvaluationInput(calibration Calibration, value TimeOfDay) error {
	if calibration.stripLength <= 0 {
		return fmt.Errorf("evaluate calibration: %w", ErrInvalidStripLength)
	}
	if len(calibration.points) < 2 {
		return fmt.Errorf("evaluate calibration: %w", ErrTooFewCalibrationPoints)
	}
	if value.nanoseconds < 0 || value.nanoseconds >= dayNanoseconds {
		return fmt.Errorf("evaluate calibration: %w", ErrInvalidTimeOfDay)
	}
	return nil
}

func interpolatePixel(from, to CalibrationPoint, query int64, stripLength int) (int, error) {
	elapsed := query - from.Time.nanoseconds
	span := to.Time.nanoseconds - from.Time.nanoseconds

	// Exact integer arithmetic avoids overflowing either the pixel subtraction
	// or the products for otherwise-valid calibrations near the int limit.
	fromPixel := big.NewInt(int64(from.Pixel))
	delta := new(big.Int).Sub(big.NewInt(int64(to.Pixel)), fromPixel)
	numerator := new(big.Int).Mul(new(big.Int).Set(fromPixel), big.NewInt(span))
	numerator.Add(numerator, new(big.Int).Mul(delta, big.NewInt(elapsed)))
	result, remainder := new(big.Int), new(big.Int)
	result.QuoRem(numerator, big.NewInt(span), remainder)
	if new(big.Int).Lsh(new(big.Int).Set(remainder), 1).Cmp(big.NewInt(span)) >= 0 {
		result.Add(result, big.NewInt(1))
	}
	if !result.IsInt64() {
		return 0, fmt.Errorf("evaluate calibration: %w: interpolated result cannot be represented", ErrPixelOutOfRange)
	}
	return checkedPixel(int(result.Int64()), stripLength)
}

func checkedPixel(pixel, stripLength int) (int, error) {
	if pixel < 0 || pixel >= stripLength {
		return 0, fmt.Errorf("evaluate calibration: %w: %d outside [0, %d)", ErrPixelOutOfRange, pixel, stripLength)
	}
	return pixel, nil
}
