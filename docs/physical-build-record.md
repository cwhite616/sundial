# Physical RGBW Build Record

## Fixed configuration

- Target: Raspberry Pi Zero W, Raspbian, 32-bit Linux/ARM, Go 1.26.6 with cgo enabled. Linux/ARM64 intentionally uses the portable simulator rather than the native adapter.
- Execution: run as root with `sudo`; the native PWM driver requires privileged hardware access.
- Strip: 144 SK6812 RGBW pixels, configured as GRBW on BCM GPIO18 (physical pin 12).
- Native binding: `github.com/rpi-ws281x/rpi-ws281x-go` v1.0.10.
- Native timing: library defaults, 800 kHz output frequency and DMA channel 10.
- Native brightness ceiling: 96/255.
- Power: one shared 5 V/2.5 A supply feeding the Pi through header pins 2 (5 V) and 6 (ground), with a common strip ground.
- Software strip-current budget: 500 mA. The renderer uses 20 coefficient units per channel step and a 127,500-unit budget (`500 * 255`) for each 20 mA-at-full-scale R/G/B/W channel model.
- Startup/shutdown: initialize with a dark render; on shutdown attempt another dark render before releasing the native driver.

The software limits are conservative controls, not electrical certification. Verify wiring, signal conditioning, data-line protection, power buffering, supply capability, and actual current independently before unattended use.

## Provisional palette

Values use the adapter's `WWRRGGBB` packed representation. They are diagnostic starting points, not calibrated product colors; centralized safety scaling may uniformly reduce them.

| Semantic color | Packed value |
| --- | --- |
| Blue Sun | `905F00A0` |
| Red Sun | `90A03500` |
| Green Sun | `9020A020` |
| Lightning | `FFF0E0FF` |
| Rain | `503000B0` |
| Snow | `D0A0C0FF` |

## Hardware verification log

Hardware verification was not available during implementation. On the target, record the date, Raspbian release (`cat /etc/os-release`), architecture (`uname -m`), Go version (`/usr/local/go/bin/go version`), and the results below.

1. Run `sudo /usr/local/go/bin/go test ./...` and record the result.
2. Confirm the strip is dark before starting the process.
3. Run `sudo /usr/local/go/bin/go run ./cmd/sundial`. Startup automatically holds individually bounded red, green, blue, and dedicated-white frames for 750 ms each, then leaves the provisional Red Sun preview active. Record whether each physical channel matches the configured GRBW order.
5. Press Ctrl-C, confirm the strip becomes dark, and record any clear or release error.
6. Record measured current, viewing limitations, unexpected flicker/color, and any mismatch. Do not accept the story if a channel mismatch, over-budget measurement, or fail-dark shutdown problem is observed.

| Check | Observation |
| --- | --- |
| OS / architecture / Go | Pending target run |
| Resolved frequency / DMA | Expected defaults: 800 kHz / DMA 10; confirm on target |
| Red diagnostic | Pending target run |
| Green diagnostic | Pending target run |
| Blue diagnostic | Pending target run |
| Dedicated-white diagnostic | Pending target run |
| Ctrl-C dark shutdown | Pending target run |
| Current and limitations | Pending measurement |
