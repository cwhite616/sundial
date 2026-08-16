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

## Status

Early development. The repository currently contains the Go module scaffold; hardware prototyping and initial clock behavior come next.

## Future ideas

After the core clock works, possible additions include:

- API-based calibration and control
- Ambient temperature-trend colors
- Rain and snow animations
- Cloud-cover dimming and subtle lightning effects
- Home Assistant integration
- A finished presentation-quality mount with concealed electronics

