package clock

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidCivilTime     = errors.New("invalid civil time")
	ErrAmbiguousCivilTime   = errors.New("ambiguous civil time")
	ErrNonexistentCivilTime = errors.New("nonexistent civil time")
)

// CivilTime contains strict Gregorian calendar and local-clock fields.
type CivilTime struct {
	Year, Month, Day                 int
	Hour, Minute, Second, Nanosecond int
}

// ResolveCivilTime resolves fields in location only when exactly one instant
// round-trips to those same fields.
func ResolveCivilTime(c CivilTime, location *time.Location) (time.Time, error) {
	if location == nil {
		return time.Time{}, fmt.Errorf("%w: nil location", ErrInvalidCivilTime)
	}
	if c.Year < 1 || c.Month < 1 || c.Month > 12 || c.Day < 1 || c.Hour < 0 || c.Hour > 23 || c.Minute < 0 || c.Minute > 59 || c.Second < 0 || c.Second > 59 || c.Nanosecond < 0 || c.Nanosecond >= int(time.Second) {
		return time.Time{}, fmt.Errorf("%w: fields out of range", ErrInvalidCivilTime)
	}
	naive := time.Date(c.Year, time.Month(c.Month), c.Day, c.Hour, c.Minute, c.Second, c.Nanosecond, time.UTC)
	if naive.Year() != c.Year || int(naive.Month()) != c.Month || naive.Day() != c.Day {
		return time.Time{}, fmt.Errorf("%w: invalid calendar date", ErrInvalidCivilTime)
	}

	matches := make([]time.Time, 0, 2)
	// IANA UTC offsets have integer-second precision. Exhausting the full legal
	// range avoids assumptions about transition duration or nearby offsets.
	const maximumUTCOffsetSeconds = 24 * 60 * 60
	for offset := -maximumUTCOffsetSeconds; offset <= maximumUTCOffsetSeconds; offset++ {
		candidate := naive.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		_, actualOffset := local.Zone()
		if actualOffset == offset && sameCivil(local, c) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return time.Time{}, fmt.Errorf("%w: %04d-%02d-%02d %02d:%02d:%02d", ErrNonexistentCivilTime, c.Year, c.Month, c.Day, c.Hour, c.Minute, c.Second)
	}
	if len(matches) > 1 {
		return time.Time{}, fmt.Errorf("%w: %04d-%02d-%02d %02d:%02d:%02d", ErrAmbiguousCivilTime, c.Year, c.Month, c.Day, c.Hour, c.Minute, c.Second)
	}
	return matches[0].In(location), nil
}

func sameCivil(value time.Time, c CivilTime) bool {
	return value.Year() == c.Year && int(value.Month()) == c.Month && value.Day() == c.Day && value.Hour() == c.Hour && value.Minute() == c.Minute && value.Second() == c.Second && value.Nanosecond() == c.Nanosecond
}
