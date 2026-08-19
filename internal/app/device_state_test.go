package app

import (
	"errors"
	"testing"

	"github.com/cwhite616/sundial/internal/clock"
)

func TestDeviceStateValidatesAndOwnsReplacement(t *testing.T) {
	points := testPoints(t, 1, 8)
	state, err := NewDeviceState(7, Replacement{PreferredZone: "America/Detroit", StripLength: 10, Points: points})
	if err != nil {
		t.Fatal(err)
	}
	points[0].Pixel = 9
	snapshotPoints := state.Calibration().Points()
	snapshotPoints[1].Pixel = 0
	if state.Revision() != 7 || state.PreferredZone() != "America/Detroit" || state.Location().String() != "America/Detroit" {
		t.Fatalf("unexpected state metadata: revision=%d zone=%q location=%v", state.Revision(), state.PreferredZone(), state.Location())
	}
	got := state.Calibration().Points()
	if got[0].Pixel != 1 || got[1].Pixel != 8 {
		t.Fatalf("state points mutated through caller: %+v", got)
	}
}

func TestDeviceStateRejectsInvalidZoneAndCalibration(t *testing.T) {
	if _, err := NewDeviceState(0, Replacement{PreferredZone: "not/a-zone", StripLength: 10, Points: testPoints(t, 1, 8)}); !errors.Is(err, ErrInvalidPreferredZone) {
		t.Fatalf("zone error = %v; want ErrInvalidPreferredZone", err)
	}
	if _, err := NewDeviceState(0, Replacement{PreferredZone: "UTC", StripLength: 1, Points: testPoints(t, 0, 1)}); !errors.Is(err, clock.ErrPixelOutOfRange) {
		t.Fatalf("calibration error = %v; want ErrPixelOutOfRange", err)
	}
}

func testPoints(t *testing.T, firstPixel, secondPixel int) []clock.CalibrationPoint {
	t.Helper()
	first, err := clock.NewTimeOfDay(6, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := clock.NewTimeOfDay(18, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return []clock.CalibrationPoint{{Time: first, Pixel: firstPixel}, {Time: second, Pixel: secondPixel}}
}
