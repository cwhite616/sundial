// Package rpiws281x adapts logical RGBW frames to an rpi-ws281x backend.
// The native binding itself lives in a target-only file.
package rpiws281x

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cwhite616/sundial/internal/render"
)

var ErrClosed = errors.New("rpi-ws281x LED driver is closed")

type backend interface {
	Init() error
	Render([]uint32) error
	Release() error
}

type Driver struct {
	mu       sync.Mutex
	length   int
	backend  backend
	closed   bool
	closeErr error
}

func newDriver(ctx context.Context, length int, makeBackend func() (backend, error)) (*Driver, error) {
	if length <= 0 {
		return nil, fmt.Errorf("initialize rpi-ws281x LED driver: strip length must be positive: %d", length)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("initialize rpi-ws281x LED driver: %w", err)
	}
	b, err := makeBackend()
	if err != nil {
		return nil, fmt.Errorf("create native rpi-ws281x backend: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize rpi-ws281x LED driver: %w", err), releaseError(b))
	}
	if err := b.Init(); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize native rpi-ws281x backend: %w", err), releaseError(b))
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize rpi-ws281x LED driver: %w", err), clearAndRelease(b, length))
	}
	if err := b.Render(make([]uint32, length)); err != nil {
		return nil, errors.Join(fmt.Errorf("render initial dark rpi-ws281x frame: %w", err), clearAndRelease(b, length))
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize rpi-ws281x LED driver after dark render: %w", err), clearAndRelease(b, length))
	}
	return &Driver{length: length, backend: b}, nil
}

func pack(pixel render.Pixel) uint32 {
	return uint32(pixel.W)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
}

func (d *Driver) WriteFrame(ctx context.Context, frame render.Frame) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write rpi-ws281x frame: %w", err)
	}
	if frame.Len() != d.length {
		return fmt.Errorf("write rpi-ws281x frame: length %d does not match strip length %d", frame.Len(), d.length)
	}
	packed := make([]uint32, d.length)
	for i, pixel := range frame.Pixels() {
		packed[i] = pack(pixel)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write rpi-ws281x frame: %w", err)
	}
	if err := d.backend.Render(packed); err != nil {
		return fmt.Errorf("render native rpi-ws281x frame: %w", err)
	}
	return nil
}

func (d *Driver) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("clear rpi-ws281x LED driver: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("clear rpi-ws281x LED driver: %w", err)
	}
	if err := d.backend.Render(make([]uint32, d.length)); err != nil {
		return fmt.Errorf("render dark rpi-ws281x frame: %w", err)
	}
	return nil
}

func (d *Driver) Close(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return d.closeErr
	}
	d.closed = true
	var errs []error
	if err := ctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("clear rpi-ws281x LED driver before release: %w", err))
	} else if err := d.backend.Render(make([]uint32, d.length)); err != nil {
		errs = append(errs, fmt.Errorf("render dark rpi-ws281x frame before release: %w", err))
	}
	if err := d.backend.Release(); err != nil {
		errs = append(errs, fmt.Errorf("release native rpi-ws281x backend: %w", err))
	}
	d.closeErr = errors.Join(errs...)
	return d.closeErr
}

func releaseError(b backend) error {
	if err := b.Release(); err != nil {
		return fmt.Errorf("release native rpi-ws281x backend: %w", err)
	}
	return nil
}

func clearAndRelease(b backend, length int) error {
	var errs []error
	if err := b.Render(make([]uint32, length)); err != nil {
		errs = append(errs, fmt.Errorf("render dark rpi-ws281x frame before release: %w", err))
	}
	if err := releaseError(b); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
