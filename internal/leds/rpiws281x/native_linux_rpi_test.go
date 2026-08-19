//go:build linux && (arm || arm64) && cgo

package rpiws281x

import (
	"testing"

	ws2811 "github.com/rpi-ws281x/rpi-ws281x-go"
)

func TestNativeGRBWMapping(t *testing.T) {
	got, err := nativeStripValue(nativeStripGRBW)
	if err != nil {
		t.Fatal(err)
	}
	if got != ws2811.SK6812StripGRBW {
		t.Fatalf("native strip type = %X, want GRBW %X", got, ws2811.SK6812StripGRBW)
	}
}
