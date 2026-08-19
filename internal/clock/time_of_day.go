// Package clock contains the pure domain model for Sundial's daily clock.
package clock

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidTimeOfDay = errors.New("invalid time of day")

// TimeOfDay is a comparable local-clock value in the half-open range [0, 24h).
// It has no date or UTC offset.
type TimeOfDay struct{ nanoseconds int64 }

// NewTimeOfDay constructs a time of day from strict local clock fields.
func NewTimeOfDay(hour, minute, second, nanosecond int) (TimeOfDay, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 || nanosecond < 0 || nanosecond >= int(time.Second) {
		return TimeOfDay{}, fmt.Errorf("%w: %02d:%02d:%02d.%09d", ErrInvalidTimeOfDay, hour, minute, second, nanosecond)
	}
	return TimeOfDay{nanoseconds: int64(hour)*int64(time.Hour) + int64(minute)*int64(time.Minute) + int64(second)*int64(time.Second) + int64(nanosecond)}, nil
}

// TimeOfDayFromDuration constructs a value from an elapsed duration since local midnight.
func TimeOfDayFromDuration(value time.Duration) (TimeOfDay, error) {
	if value < 0 || value >= 24*time.Hour {
		return TimeOfDay{}, fmt.Errorf("%w: duration %s is outside [0, 24h)", ErrInvalidTimeOfDay, value)
	}
	return TimeOfDay{nanoseconds: int64(value)}, nil
}

// TimeOfDayFromTime reduces an already-resolved instant using its local clock fields.
func TimeOfDayFromTime(value time.Time) TimeOfDay {
	hour, minute, second := value.Clock()
	result, _ := NewTimeOfDay(hour, minute, second, value.Nanosecond())
	return result
}

func (t TimeOfDay) Hour() int               { return int(time.Duration(t.nanoseconds) / time.Hour) }
func (t TimeOfDay) Minute() int             { return int(time.Duration(t.nanoseconds) % time.Hour / time.Minute) }
func (t TimeOfDay) Second() int             { return int(time.Duration(t.nanoseconds) % time.Minute / time.Second) }
func (t TimeOfDay) Nanosecond() int         { return int(time.Duration(t.nanoseconds) % time.Second) }
func (t TimeOfDay) Duration() time.Duration { return time.Duration(t.nanoseconds) }
