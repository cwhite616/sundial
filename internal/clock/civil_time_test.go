package clock

import (
	"errors"
	"testing"
	"time"
)

func TestResolveCivilTime(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	ordinary := CivilTime{Year: 2025, Month: 1, Day: 15, Hour: 12, Minute: 34, Second: 56, Nanosecond: 789}
	resolved, err := ResolveCivilTime(ordinary, location)
	if err != nil {
		t.Fatal(err)
	}
	if !sameCivil(resolved, ordinary) {
		t.Fatalf("resolved = %v", resolved)
	}
	wantUTC := time.Date(2025, 1, 15, 17, 34, 56, 789, time.UTC)
	if !resolved.Equal(wantUTC) || resolved.Location() != location {
		t.Fatalf("resolved = %v in %v, want instant %v in %v", resolved, resolved.Location(), wantUTC, location)
	}
	name, offset := resolved.Zone()
	if name != "EST" || offset != -5*60*60 {
		t.Fatalf("zone = %s %d, want EST -18000", name, offset)
	}

	for _, test := range []struct {
		name  string
		civil CivilTime
		want  error
	}{
		{"invalid date", CivilTime{Year: 2025, Month: 2, Day: 29}, ErrInvalidCivilTime},
		{"year zero", CivilTime{Year: 0, Month: 1, Day: 1}, ErrInvalidCivilTime},
		{"month zero", CivilTime{Year: 2025, Month: 0, Day: 1}, ErrInvalidCivilTime},
		{"month overflow", CivilTime{Year: 2025, Month: 13, Day: 1}, ErrInvalidCivilTime},
		{"day zero", CivilTime{Year: 2025, Month: 1, Day: 0}, ErrInvalidCivilTime},
		{"invalid 30-day date", CivilTime{Year: 2025, Month: 4, Day: 31}, ErrInvalidCivilTime},
		{"normalized hour", CivilTime{Year: 2025, Month: 1, Day: 1, Hour: 24}, ErrInvalidCivilTime},
		{"negative minute", CivilTime{Year: 2025, Month: 1, Day: 1, Minute: -1}, ErrInvalidCivilTime},
		{"minute overflow", CivilTime{Year: 2025, Month: 1, Day: 1, Minute: 60}, ErrInvalidCivilTime},
		{"negative second", CivilTime{Year: 2025, Month: 1, Day: 1, Second: -1}, ErrInvalidCivilTime},
		{"second overflow", CivilTime{Year: 2025, Month: 1, Day: 1, Second: 60}, ErrInvalidCivilTime},
		{"negative nanosecond", CivilTime{Year: 2025, Month: 1, Day: 1, Nanosecond: -1}, ErrInvalidCivilTime},
		{"nanosecond overflow", CivilTime{Year: 2025, Month: 1, Day: 1, Nanosecond: int(time.Second)}, ErrInvalidCivilTime},
		{"gap", CivilTime{Year: 2025, Month: 3, Day: 9, Hour: 2, Minute: 30}, ErrNonexistentCivilTime},
		{"fold", CivilTime{Year: 2025, Month: 11, Day: 2, Hour: 1, Minute: 30}, ErrAmbiguousCivilTime},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := ResolveCivilTime(test.civil, location)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if !value.IsZero() {
				t.Fatalf("partial instant = %v", value)
			}
		})
	}
}

func TestResolveCivilTimeNilLocation(t *testing.T) {
	got, err := ResolveCivilTime(CivilTime{Year: 2025, Month: 1, Day: 1}, nil)
	if !errors.Is(err, ErrInvalidCivilTime) || !got.IsZero() {
		t.Fatalf("result = %v, %v", got, err)
	}
}

func TestResolveCivilTimeNonHourTransitionAndSkippedDate(t *testing.T) {
	lordHowe, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Fatal(err)
	}
	// Lord Howe advances by thirty minutes, skipping 02:00 through 02:29.
	if got, err := ResolveCivilTime(CivilTime{Year: 2025, Month: 10, Day: 5, Hour: 2, Minute: 15}, lordHowe); !errors.Is(err, ErrNonexistentCivilTime) || !got.IsZero() {
		t.Fatalf("Lord Howe gap = %v, %v", got, err)
	}
	if got, err := ResolveCivilTime(CivilTime{Year: 2025, Month: 4, Day: 6, Hour: 1, Minute: 45}, lordHowe); !errors.Is(err, ErrAmbiguousCivilTime) || !got.IsZero() {
		t.Fatalf("Lord Howe fold = %v, %v", got, err)
	}

	apia, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Fatal(err)
	}
	// Samoa's 2011 date-line move skipped the whole local date December 30.
	if got, err := ResolveCivilTime(CivilTime{Year: 2011, Month: 12, Day: 30, Hour: 12}, apia); !errors.Is(err, ErrNonexistentCivilTime) || !got.IsZero() {
		t.Fatalf("Apia skipped date = %v, %v", got, err)
	}
}
