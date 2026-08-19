package clock

import (
	"errors"
	"testing"
	"time"
)

func TestNewTimeOfDayBounds(t *testing.T) {
	valid, err := NewTimeOfDay(23, 59, 59, 999999999)
	if err != nil || valid.Duration() != 24*time.Hour-1 {
		t.Fatalf("last instant = %v, %v", valid, err)
	}
	for _, fields := range [][4]int{{-1, 0, 0, 0}, {24, 0, 0, 0}, {0, 60, 0, 0}, {0, 0, 60, 0}, {0, 0, 0, 1e9}} {
		if _, err := NewTimeOfDay(fields[0], fields[1], fields[2], fields[3]); !errors.Is(err, ErrInvalidTimeOfDay) {
			t.Errorf("fields %v error = %v", fields, err)
		}
	}
}

func TestTimeOfDayFromTimeUsesLocalFields(t *testing.T) {
	location, _ := time.LoadLocation("America/New_York")
	first := time.Date(2025, 11, 2, 5, 30, 0, 123, time.UTC).In(location)
	second := first.Add(time.Hour)
	if first.Format("15:04") != "01:30" || second.Format("15:04") != "01:30" {
		t.Fatal("test data does not straddle repeated local time")
	}
	if TimeOfDayFromTime(first) != TimeOfDayFromTime(second) {
		t.Fatal("repeated local readings reduced differently")
	}
}

func TestTimeOfDayFromDurationBoundsAndAccessors(t *testing.T) {
	for _, test := range []struct {
		name  string
		value time.Duration
		want  error
	}{
		{"midnight", 0, nil},
		{"last nanosecond", 24*time.Hour - time.Nanosecond, nil},
		{"negative", -time.Nanosecond, ErrInvalidTimeOfDay},
		{"exactly 24 hours", 24 * time.Hour, ErrInvalidTimeOfDay},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := TimeOfDayFromDuration(test.value)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.want == nil && got.Duration() != test.value {
				t.Fatalf("duration = %v, want %v", got.Duration(), test.value)
			}
		})
	}

	value, err := NewTimeOfDay(17, 23, 41, 987654321)
	if err != nil {
		t.Fatal(err)
	}
	if value.Hour() != 17 || value.Minute() != 23 || value.Second() != 41 || value.Nanosecond() != 987654321 {
		t.Fatalf("accessors returned %02d:%02d:%02d.%09d", value.Hour(), value.Minute(), value.Second(), value.Nanosecond())
	}
}
