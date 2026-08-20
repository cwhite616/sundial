# Sundial

Sundial is a playful hardware-and-software project that makes a cast-iron sundial work indoors. A Raspberry Pi Zero W controls an SK6812 RGBW LED strip positioned around the sundial. The active light acts as an artificial sun, casting a shadow from the gnomon that indicates the approximate current time.

The goal is kinetic art, not a precision clock: the sundial should remain the focus while the electronics disappear into the illusion.

## Project goals

- Build a working physical sundial for an indoor space.
- Learn Go by developing the control software.
- Keep the clock functional without a cloud dependency.
- Make the hardware and software easy to calibrate and evolve independently.
- Eventually add subtle ambient weather effects without compromising time readability.

## MVP

The first milestone is an adjustable cardboard or foam-core prototype that:

- Runs on a Raspberry Pi Zero W.
- Drives an SK6812 RGBW LED strip as an artificial sun.
- Displays time through the gnomon's shadow with accuracy within about 30 minutes.
- Supports iterative calibration through code and physical adjustment.
- Starts operating when powered on and continues working at night.
- Uses network time synchronization when available and falls back to the system clock when offline.

A finished mount, API-based calibration, smart-home integrations, and weather effects are deferred until after the prototype works.

## Planned hardware

- Raspberry Pi Zero W
- SK6812 RGBW LED strip
- Suitable 5 V power supply
- Logic-level shifter
- Data-line resistor and power capacitor
- Adjustable cardboard or foam-core mounting structure
- Supporting wiring and connectors

Exact geometry, LED spacing, brightness limits, and supporting components will be refined through prototyping and calibration.

## Software

The control software is being written in Go. Development normally happens on macOS, with deployment targeting the Raspberry Pi Zero W.

The project is expected to separate timekeeping and calibration behavior from LED hardware access so that core logic can be developed and tested without the physical device attached.

## Native Raspberry Pi service

Sundial runs as one native process; it does not need a login session or a
container. A native build performed on the Raspberry Pi selects the physical
`rpi-ws281x` adapter:

```sh
CGO_ENABLED=1 go build -o sundial ./cmd/sundial
sudo groupadd --system sundial
sudo useradd --system --gid sundial --home-dir /var/lib/sundial --shell /usr/sbin/nologin sundial
sudo install -o root -g root -m 0755 sundial /usr/local/bin/sundial
sudo install -d -o root -g root -m 0755 /etc/sundial
sudo install -o root -g root -m 0644 config/sundial.example.json /etc/sundial/config.json
sudo install -d -o sundial -g sundial -m 0750 /var/lib/sundial
sudo install -o sundial -g sundial -m 0640 config/state.example.json /var/lib/sundial/state.json
sudo install -o root -g root -m 0644 deploy/sundial.service /etc/systemd/system/sundial.service
sudo systemctl daemon-reload
sudo systemctl enable --now sundial.service
```

For an ARMv6 cross-build, `GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=1` is
necessary but not sufficient: configure `CC` to an ARM Linux cross-compiler
with the native ws281x library/toolchain available. A normal macOS compiler
cannot produce this cgo-backed target binary.

The configuration at `/etc/sundial/config.json` is read-only service input.
The separately permissioned `/var/lib/sundial/state.json` is writable durable
calibration and preferred-zone state (schema version 1). Install a valid state
file before starting the unit. A non-ARM or non-cgo binary rejects physical
startup explicitly; it never silently substitutes simulated output.

Safety current values share one caller-selected integer unit. Derive each
channel coefficient conservatively as that channel's measured worst-case
full-scale current in the chosen unit divided by 255 (rounding up), and express
`max_strip_current` in the same unit. `brightness_ceiling` is the separate
global 1–255 scale ceiling. Account for power-supply, wiring, connector, and
thermal limits when choosing the strip budget; do not copy the example values
without validating the installed hardware.

Use `sudo systemctl stop sundial` for graceful SIGTERM shutdown and
`journalctl -u sundial.service -f` for newline-delimited JSON lifecycle,
synchronization, and output diagnostics. After the process exits unsuccessfully,
the unit waits ten seconds before restarting it and limits repeated startup failures to three per five
minutes, preventing invalid input from creating an uncontrolled restart loop.

## Future ideas

After the core clock works, possible additions include:

- API-based calibration and control
- Ambient temperature-trend colors
- Rain and snow animations
- Cloud-cover dimming and subtle lightning effects
- Home Assistant integration
- A finished presentation-quality mount with concealed electronics
