package rpiws281x

import "fmt"

type nativeStripType uint8

const nativeStripGRBW nativeStripType = iota + 1

type nativeConfig struct {
	GPIO       int
	Length     int
	Brightness int
	StripType  nativeStripType
}

func newNativeConfig(length, brightness int) (nativeConfig, error) {
	if length <= 0 {
		return nativeConfig{}, fmt.Errorf("configure native rpi-ws281x: strip length must be positive: %d", length)
	}
	if brightness < 0 || brightness > 255 {
		return nativeConfig{}, fmt.Errorf("configure native rpi-ws281x: brightness %d outside [0, 255]", brightness)
	}
	return nativeConfig{GPIO: 18, Length: length, Brightness: brightness, StripType: nativeStripGRBW}, nil
}

func copyNativeFrame(destination, frame []uint32) error {
	if len(destination) != len(frame) {
		return fmt.Errorf("copy native rpi-ws281x frame: LED buffer length %d does not match frame length %d", len(destination), len(frame))
	}
	copy(destination, frame)
	return nil
}
