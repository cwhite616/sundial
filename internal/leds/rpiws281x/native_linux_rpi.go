//go:build linux && (arm || arm64) && cgo

package rpiws281x

import (
	"context"
	"fmt"

	ws2811 "github.com/rpi-ws281x/rpi-ws281x-go"
)

const nativeChannel = 0

type nativeBackend struct{ device *ws2811.WS2811 }

// New configures GPIO18 (physical pin 12), SK6812 GRBW ordering, and the
// binding's default DMA and frequency. Brightness is an independent hardware
// ceiling in addition to the renderer's centralized current limit.
func New(ctx context.Context, length, brightness int) (*Driver, error) {
	config, err := newNativeConfig(length, brightness)
	if err != nil {
		return nil, err
	}
	return newDriver(ctx, length, func() (backend, error) {
		options := ws2811.DefaultOptions
		if len(options.Channels) <= nativeChannel {
			return nil, fmt.Errorf("configure native rpi-ws281x: default options have no channel %d", nativeChannel)
		}
		options.Channels = append([]ws2811.ChannelOption(nil), options.Channels...)
		options.Channels[nativeChannel].GpioPin = config.GPIO
		options.Channels[nativeChannel].LedCount = config.Length
		options.Channels[nativeChannel].Brightness = config.Brightness
		stripType, err := nativeStripValue(config.StripType)
		if err != nil {
			return nil, err
		}
		options.Channels[nativeChannel].StripeType = stripType
		device, err := ws2811.MakeWS2811(&options)
		if err != nil {
			return nil, err
		}
		return &nativeBackend{device: device}, nil
	})
}

func nativeStripValue(stripType nativeStripType) (int, error) {
	if stripType != nativeStripGRBW {
		return 0, fmt.Errorf("configure native rpi-ws281x: unsupported strip type %d", stripType)
	}
	return ws2811.SK6812StripGRBW, nil
}

func (b *nativeBackend) Init() error { return b.device.Init() }
func (b *nativeBackend) Render(pixels []uint32) error {
	if err := copyNativeFrame(b.device.Leds(nativeChannel), pixels); err != nil {
		return err
	}
	return b.device.Render()
}
func (b *nativeBackend) Release() error {
	b.device.Fini()
	return nil
}
