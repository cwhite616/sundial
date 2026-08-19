package app

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cwhite616/sundial/internal/clock"
)

var (
	ErrInvalidPreferredZone = errors.New("invalid preferred zone")
	ErrRevisionExhausted    = errors.New("device state revision exhausted")
)

// Replacement is an unmaterialized request to replace all durable device
// state. The controller copies Points before admitting the request.
type Replacement struct {
	PreferredZone string
	StripLength   int
	Points        []clock.CalibrationPoint
}

// DeviceState is an immutable snapshot of all durable device state.
type DeviceState struct {
	revision      uint64
	preferredZone string
	location      *time.Location
	calibration   clock.Calibration
}

func NewDeviceState(revision uint64, replacement Replacement) (DeviceState, error) {
	if replacement.PreferredZone == "" || replacement.PreferredZone == "Local" {
		return DeviceState{}, fmt.Errorf("%w %q", ErrInvalidPreferredZone, replacement.PreferredZone)
	}
	location, err := time.LoadLocation(replacement.PreferredZone)
	if err != nil {
		return DeviceState{}, fmt.Errorf("load preferred zone %q: %w", replacement.PreferredZone, errors.Join(ErrInvalidPreferredZone, err))
	}
	calibration, err := clock.NewCalibration(replacement.StripLength, replacement.Points)
	if err != nil {
		return DeviceState{}, fmt.Errorf("validate device-state calibration: %w", err)
	}
	return DeviceState{
		revision:      revision,
		preferredZone: replacement.PreferredZone,
		location:      location,
		calibration:   calibration,
	}, nil
}

func (s DeviceState) Revision() uint64               { return s.revision }
func (s DeviceState) PreferredZone() string          { return s.preferredZone }
func (s DeviceState) Location() *time.Location       { return s.location }
func (s DeviceState) Calibration() clock.Calibration { return s.calibration }

func (s DeviceState) replacement() Replacement {
	return Replacement{
		PreferredZone: s.preferredZone,
		StripLength:   s.calibration.StripLength(),
		Points:        s.calibration.Points(),
	}
}

func (s DeviceState) validate() error {
	_, err := NewDeviceState(s.revision, s.replacement())
	return err
}

func nextRevision(current uint64) (uint64, error) {
	if current == math.MaxUint64 {
		return 0, ErrRevisionExhausted
	}
	return current + 1, nil
}
