//go:build linux && (arm || arm64) && cgo

package rpiws281x

import (
	"context"

	ws2811 "github.com/rpi-ws281x/rpi-ws281x-go"
)

const nativeChannel = 0

type nativeBackend struct{ device *ws2811.WS2811 }

// New configures GPIO18 (physical pin 12), SK6812 GRBW ordering, and the
// binding's default DMA and frequency. Brightness is an independent hardware
// ceiling in addition to the renderer's centralized current limit.
func New(ctx context.Context, length, brightness int) (*Driver, error) {
	return newDriver(ctx, length, func() (backend, error) {
		options := ws2811.DefaultOptions
		options.Channels = append([]ws2811.ChannelOption(nil), options.Channels...)
		options.Channels[nativeChannel].GpioPin = 18
		options.Channels[nativeChannel].LedCount = length
		options.Channels[nativeChannel].Brightness = brightness
		options.Channels[nativeChannel].StripeType = ws2811.SK6812StripGRBW
		device, err := ws2811.MakeWS2811(&options)
		if err != nil {
			return nil, err
		}
		return &nativeBackend{device: device}, nil
	})
}

func (b *nativeBackend) Init() error { return b.device.Init() }
func (b *nativeBackend) Render(pixels []uint32) error {
	copy(b.device.Leds(nativeChannel), pixels)
	return b.device.Render()
}
func (b *nativeBackend) Release() error {
	b.device.Fini()
	return nil
}
