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

## Acceptance-setup revisions

Every physical experiment must name one acceptance-setup revision. A revision is an immutable record of the conditions that could affect shadow position, readability, or electrical safety; corrections are added as notes or captured in a new revision rather than silently overwriting the original observation.

Create a new revision whenever any of these materially changes:

- mount geometry, including LED height, radius, active spacing, alignment, or orientation;
- the reference viewing position;
- the ambient-light regime;
- the supply, common-ground arrangement, signal conditioning, data-line protection, power buffering, or supply protection; or
- the configured current limit, renderer brightness ceiling, native brightness, strip configuration, or output geometry definition.

Results from different revisions are not directly comparable. A comparison may describe a qualitative difference, but it must identify both revisions and the changed conditions. Photos are supplemental evidence only; measurements and written observations remain required.

Use millimetres for physical dimensions and measure them from these references:

- **Height:** perpendicular distance from the sundial's dial plane to the center of the active LED emitter.
- **Radius:** straight-line distance in the dial plane from the gnomon's base to the active LED emitter's projected center.
- **Active spacing:** center-to-center distance between adjacent active LED emitters, measured along the mounted strip.
- **Alignment/orientation:** identify pixel-number direction and record the active emitter's position relative to the gnomon's base and dial plane.
- **Viewing position:** record eye height and horizontal displacement from the gnomon's base, plus viewing direction. Mark the floor or support so it can be reproduced.

### AS-2026-08-19-R0 — assembled; acceptance pending

This revision records Charlie's approximate measurements and observations of the assembled prototype together with the attributable electrical evidence established by the accepted hardware verification below. Continued supervised prototyping is authorized, but this revision is **not finally accepted**: the planned logic-level shifter has not yet been installed or inspected, and the CanaKit supply's rating and protection details have not been established. Unknown fields are intentionally not inferred from planned hardware or software configuration.

| Field | Attributable value / status |
| --- | --- |
| Date / observer | 2026-08-19 / Charlie |
| Temporary construction and attachment | Prototype assembled on a roughly 18 in (approximately 457 mm) wood-block base. A half-circle wire support runs end-to-end; about 120 LEDs are taped to the wire and the remaining approximately 24 lie on the wood. No permanent alteration of the cast-iron sundial was reported; attachment/removability still requires final inspection. Wood differs from the planned cardboard/foam-core material and must be reviewed at final acceptance. |
| Independent adjustment | LED tape on wire is temporary, but independent adjustability of height, radius, active spacing, and alignment has not yet been demonstrated or inspected. |
| Strip and output | 144 SK6812 RGBW pixels, GRBW order, BCM GPIO18 (physical pin 12) |
| Active LED count and spacing | About 120 of 144 LEDs mounted on the wire arc; remainder lie on the wood base. Strip density is 144 LEDs/m, implying a nominal pitch of approximately 6.94 mm; actual mounted center-to-center spacing has not been measured. Single-pixel previews illuminate one LED at a time. |
| Height | Arc reported approximately 18 in (approximately 457 mm) high; exact height from dial plane to active-emitter center remains unmeasured. |
| Radius | Arc reported about 18 in (approximately 457 mm) from the gnomon; exact radius from gnomon base to projected active-emitter center remains unmeasured. |
| Alignment / orientation | Half-circle wire runs end-to-end and is angled about 20 degrees from perpendicular. The gnomon's low point is at the circle center and its high point points away from the circle. Preview uses zero-based pixel 72. Pixel-number direction and the reference used for the reported 20-degree angle remain undocumented. |
| Reference viewing position | Charlie observed that the sundial sits at the lamp base and that the shadow appears the same from any viewing angle. This is an observation for this setup, not a claim that viewing angle is universally irrelevant. A measured reproducible eye position has not been recorded. |
| Representative test marks / times | Dial marks `IV`, `II`, `XII`, `X`, and `VIII`; corresponding civil test times/time zone have not been recorded. |
| Ambient-light conditions | Room light; fixture state, sources, intensity, and time of observation were not recorded. |
| Supply | CanaKit supply feeding the Pi. The earlier fixed configuration records a shared 5 V / 2.5 A supply, but the observed CanaKit unit's rating, strip-feed arrangement, and protection details have not been independently recorded or inspected for this revision. |
| Common ground | Reported installed; arrangement pending final inspection. |
| Signal conditioning / level shifting | Not installed. A level shifter is expected 2026-08-20; final acceptance remains pending until installation and inspection. |
| Data-line protection | 330 ohm data resistor reported installed; placement pending final inspection. |
| Power buffering | 1000 µF capacitor reported installed; voltage rating, polarity, and placement pending final inspection. |
| Configured strip-current limit | 500 mA modeled maximum; 127,500 coefficient units |
| Renderer / native brightness | Renderer ceiling 96/255; native driver 255/255 |

#### Initial single-position illumination check

The existing bounded physical preview was observed in room light. Pixel 72 was illuminated red and dedicated white during the Story 1.2 diagnostic sequence; no gnomon shadow was observable under that condition. The missing level shifter prevents final acceptance, but Charlie has authorized continued supervised prototyping until it is installed and inspected.

| Observation | Result |
| --- | --- |
| Revision / pixel | AS-2026-08-19-R0 / zero-based pixel 72 |
| Requested preview color | Story 1.2 diagnostic red (`R=255`) and dedicated white (`W=255`), each before centralized safety scaling |
| Delivered color / brightness | Each diagnostic channel value was 96 after the 96/255 renderer ceiling; native brightness 255/255 |
| Current evidence | 7.53 mA per diagnostic, conservatively modeled and not measured |
| Visible gnomon shadow | Not observable in the lit room for the pixel-72 red/white diagnostic. Charlie separately reports that a shadow is observable with the [linked gist test](https://gist.github.com/cwhite616/3951cb66186fabc42f66ece8877d8ea5), but its exact pixel, color, brightness, width, ambient conditions, and setup revision were not provided, so that report cannot replace this attributable check or be directly compared with it. |
| Reflections | Unknown; not reported |
| Diffusion | Unknown; not reported |
| Shadow softness | Unknown; not reported |
| Ambient-light limitations | A shadow was not observable under the reported room-light condition; light sources and intensity were not measured. |
| Calibration constraints / lessons | The bounded red/white diagnostic is insufficient for Calibration under the reported room light. Capture the successful gist-test settings and conditions before using its visible shadow as evidence. Final acceptance additionally requires the level shifter and inspection of all protection and attachment details. |

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
