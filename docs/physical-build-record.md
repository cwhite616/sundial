# Physical RGBW Build Record

## Fixed configuration

- Target: Raspberry Pi Zero W, Raspbian, Linux/ARM or Linux/ARM64, Go 1.26.6 with cgo enabled.
- Execution: run as root with `sudo`; the native PWM driver requires privileged hardware access.
- Strip: 144 SK6812 RGBW pixels, configured as GRBW on BCM GPIO18 (physical pin 12).
- Native binding: `github.com/rpi-ws281x/rpi-ws281x-go` v1.0.10.
- Native timing: library defaults, 800 kHz output frequency and DMA channel 10.
- Native driver brightness: 255/255. This avoids the target binding's no-output behavior at 96; it does not replace renderer safety.
- Centralized renderer brightness ceiling: 96/255, applied to every composed frame before native delivery.
- Power: one shared 5 V/2.5 A supply feeding the Pi through header pins 2 (5 V) and 6 (ground), with a common strip ground.
- Software strip-current budget: 500 mA. The renderer uses 20 coefficient units per channel step and a 127,500-unit budget (`500 * 255`) for each 20 mA-at-full-scale R/G/B/W channel model.
- Startup/shutdown: initialize with a dark render; on shutdown attempt another dark render before releasing the native driver.

The software limits are conservative controls, not electrical certification. Verify wiring, signal conditioning, data-line protection, power buffering, supply capability, and actual current independently before unattended use.

When powering through the Pi header, do not simultaneously power the Pi from USB. Use wiring and fusing appropriate to the shared supply and expected fault current. Retain a common ground, suitable logic-level shifting, data-line protection, and power buffering. These precautions reduce risk but do not constitute electrical certification.

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

## Accepted hardware verification: 2026-08-19

Final verification used the corrected Linux/ARM64 native build with native brightness 255/255, centralized renderer brightness ceiling 96/255, and the unchanged 127,500-unit current budget.

- Tester: Charlie
- Commit: `3bd0863fa9af361525c2c5bc0e63f62c625ea586`
- Commands: `sudo /usr/local/go/bin/go test ./...` and `sudo /usr/local/go/bin/go run ./cmd/sundial`

1. Run `sudo /usr/local/go/bin/go test ./...` and record the result.
2. Confirm the strip is dark before starting the process.
3. Run `sudo /usr/local/go/bin/go run ./cmd/sundial`. Startup automatically holds individually bounded red, green, blue, and dedicated-white frames for 750 ms each, then leaves the provisional Red Sun preview active. Record whether each physical channel matches the configured GRBW order.
4. Press Ctrl-C, confirm the strip becomes dark, and record any clear error. The binding's native `Fini` call exposes no release result.
5. Record measured current or conservative calculated current, identifying the method used, plus viewing limitations, unexpected flicker/color, and any mismatch. Do not accept the story if a channel mismatch, over-budget result, or fail-dark shutdown problem is observed.

| Check | Observation |
| --- | --- |
| OS / architecture / Go | Raspbian, Linux `aarch64` / Go `linux/arm64`, Go 1.26.6 with cgo enabled |
| Target tests | `sudo /usr/local/go/bin/go test ./...` passed |
| Resolved frequency / DMA | Library defaults: 800 kHz / DMA 10 |
| Pre-start state | Strip fully dark before the verification sequence |
| Red diagnostic | Correct red displayed |
| Green diagnostic | Correct green displayed |
| Blue diagnostic | Correct blue displayed |
| Dedicated-white diagnostic | Correct dedicated white displayed |
| Final preview | Provisional Red Sun displayed |
| Ctrl-C dark shutdown | Strip fully dark; no clear error observed. Native `Fini` exposes no release result. |
| Current evidence | Each diagnostic is one channel at value 96: `96 * 20 / 255 = 7.53 mA` modeled. Red Sun is W=54, R=60, G=19 after centralized scaling: `(54 + 60 + 19) * 20 / 255 = 10.43 mA` modeled. Both are below the 500 mA budget. |
| Limitations | Current is conservatively calculated from the configured worst-case model, not physically measured. No flicker, wrong colors, or runtime errors were observed. This visual verification is not electrical certification. |

## Failed target run: 2026-08-19

- Reported platform: `uname -m` = `aarch64`; Go selected `GOOS=linux GOARCH=arm64 CGO_ENABLED=1`.
- Observation: the command printed a hardware-verification success message, but no LEDs illuminated.
- Root cause: the native adapter build constraint admitted only `linux/arm`; `linux/arm64` selected the portable simulator. The original `_linux_arm.go` filenames also imposed an implicit ARM-only filename constraint. The completion message did not identify the active output mode, so it falsely implied physical verification.
- Correction: explicitly tagged, architecture-neutral `_linux_rpi.go` files make both `linux/arm` and `linux/arm64` with cgo select the native adapter. Startup prints the selected output mode. The portable path explicitly states that no physical verification occurred.
- Acceptance impact: this run provided no valid channel, current, or shutdown observation. Its pending status was resolved by the accepted hardware verification above.

## Failed dependency-lock target run: 2026-08-19

- Reported platform: `GOOS=linux GOARCH=arm64 CGO_ENABLED=1` on the Raspberry Pi target.
- Observation: the corrected native build stopped during compilation, before hardware initialization, because dependency metadata for `github.com/pkg/errors` was missing.
- Root cause: `github.com/rpi-ws281x/rpi-ws281x-go` uses `github.com/pkg/errors` transitively, but the module lock did not yet include that indirect requirement and its complete checksums. Portable host builds had not compiled the target-only native dependency and therefore did not expose the incomplete lock.
- Correction: `go mod tidy` added `github.com/pkg/errors v0.9.1 // indirect` and completed `go.sum`. Host tests, cgo-disabled tests, vet, diff checks, and `GOOS=linux GOARCH=arm64 CGO_ENABLED=1 go list -deps ./cmd/sundial` then passed.
- Acceptance impact: the process never reached the native driver, so this run provided no valid channel, current, or shutdown observation. Its pending status was resolved by the accepted hardware verification above.

## Failed target pre-run test: 2026-08-19

- Reported platform: `GOOS=linux GOARCH=arm64 CGO_ENABLED=1` on the Raspberry Pi target.
- Observation: `sudo /usr/local/go/bin/go test ./...` failed `TestPortableCompositionUsesSafeExactLengthSimulator` because native composition correctly returned an `*rpiws281x.Driver`, while the untagged test required a `*simulated.Driver`. The application was not run.
- Root cause: a portable-composition type assertion lived in the target-independent `main_test.go`, so it also ran where the native composition was selected.
- Correction: the simulator type, identity, and exact-length assertions now live in `output_portable_test.go` under the exact portable build constraint. Target-independent strip-length and safety assertions remain in `main_test.go`; ARM and ARM64 retain build-tagged native identity coverage.
- Acceptance impact: the application never reached hardware during this attempt. Its pending physical observations were resolved by the accepted hardware verification above.

## Native-brightness target finding: 2026-08-19

- Observation: with native driver brightness 96/255, the corrected physical adapter initialized but the LEDs did not illuminate. Charlie manually changed native brightness to 255/255 on the Pi and confirmed that the LEDs illuminated.
- Root cause: the application coupled the native driver's brightness setting to the renderer's 96/255 safety ceiling. On this target/binding combination, native brightness 96 produced no visible output, preventing physical verification.
- Approved correction: native driver brightness is now 255/255, while the independent centralized renderer ceiling remains 96/255 and the strip-current budget remains 127,500 coefficient units (500 mA modeled). Every frame is still safety-transformed before native delivery.
- Acceptance impact: illumination confirmed that the native output path could energize the strip. The remaining channel-order, calculated-current, and fail-dark observations were resolved by the accepted hardware verification above.
