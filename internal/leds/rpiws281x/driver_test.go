package rpiws281x

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cwhite616/sundial/internal/app"
	"github.com/cwhite616/sundial/internal/app/outputtest"
	"github.com/cwhite616/sundial/internal/render"
)

type fakeBackend struct {
	initErr, renderErr, releaseErr error
	frames                         [][]uint32
	initialized, released          bool
}

func (b *fakeBackend) Init() error { b.initialized = true; return b.initErr }
func (b *fakeBackend) Render(pixels []uint32) error {
	b.frames = append(b.frames, slices.Clone(pixels))
	err := b.renderErr
	b.renderErr = nil
	return err
}
func (b *fakeBackend) Release() error { b.released = true; return b.releaseErr }

func newFake(ctx context.Context, length int, b *fakeBackend) (*Driver, error) {
	return newDriver(ctx, length, func() (backend, error) { return b, nil })
}

func TestDriverContractAndWWRRGGBBPacking(t *testing.T) {
	var current *fakeBackend
	outputtest.Run(t, outputtest.Harness{
		New: func(length int) (app.Output, error) {
			current = &fakeBackend{}
			return newFake(context.Background(), length, current)
		},
		Snapshot: func(app.Output) render.Frame {
			pixels := make([]render.Pixel, len(current.frames[len(current.frames)-1]))
			for i, value := range current.frames[len(current.frames)-1] {
				pixels[i] = render.Pixel{W: uint8(value >> 24), R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value)}
			}
			return render.NewFrame(pixels)
		},
		InjectErrors: func(_ app.Output, errs ...error) { current.renderErr = errs[0] },
	})

	b := &fakeBackend{}
	d, err := newFake(context.Background(), 1, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFrame(context.Background(), render.NewFrame([]render.Pixel{{R: 0x11, G: 0x22, B: 0x33, W: 0x44}})); err != nil {
		t.Fatal(err)
	}
	if got := b.frames[len(b.frames)-1][0]; got != 0x44112233 {
		t.Fatalf("packed pixel = %08X, want 44112233", got)
	}
}

func TestDiagnosticChannelsPackIndependently(t *testing.T) {
	tests := []struct {
		name  string
		pixel render.Pixel
		want  uint32
	}{
		{"red", render.Pixel{R: 0xA0}, 0x00A00000},
		{"green", render.Pixel{G: 0xA0}, 0x0000A000},
		{"blue", render.Pixel{B: 0xA0}, 0x000000A0},
		{"white", render.Pixel{W: 0xA0}, 0xA0000000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pack(test.pixel); got != test.want {
				t.Fatalf("packed diagnostic = %08X, want %08X", got, test.want)
			}
		})
	}
}

func TestInitializationFailsDarkAndReleases(t *testing.T) {
	initErr := errors.New("init failed")
	b := &fakeBackend{initErr: initErr}
	if _, err := newFake(context.Background(), 2, b); !errors.Is(err, initErr) {
		t.Fatalf("init error = %v", err)
	}
	if !b.released {
		t.Fatal("backend was not released after init failure")
	}

	renderErr := errors.New("dark render failed")
	b = &fakeBackend{renderErr: renderErr}
	if _, err := newFake(context.Background(), 2, b); !errors.Is(err, renderErr) {
		t.Fatalf("dark init error = %v", err)
	}
	if !b.released || len(b.frames) != 2 {
		t.Fatalf("cleanup: released=%v frames=%d", b.released, len(b.frames))
	}
	for _, frame := range b.frames {
		for _, pixel := range frame {
			if pixel != 0 {
				t.Fatalf("initialization energized %08X", pixel)
			}
		}
	}
}

func TestCancellationNeverRendersAndClosePreservesClearAndReleaseErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &fakeBackend{}
	if _, err := newFake(ctx, 1, b); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled init = %v", err)
	}
	if len(b.frames) != 0 {
		t.Fatalf("canceled init rendered %d frames", len(b.frames))
	}

	b = &fakeBackend{}
	d, err := newFake(context.Background(), 1, b)
	if err != nil {
		t.Fatal(err)
	}
	before := len(b.frames)
	if err := d.WriteFrame(ctx, render.NewFrame([]render.Pixel{{R: 255}})); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write = %v", err)
	}
	if len(b.frames) != before {
		t.Fatal("canceled write reached backend")
	}

	clearErr, releaseErr := errors.New("clear failed"), errors.New("release failed")
	b.renderErr, b.releaseErr = clearErr, releaseErr
	err = d.Close(context.Background())
	if !errors.Is(err, clearErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("close error = %v", err)
	}
	if !b.released {
		t.Fatal("release not attempted after clear failure")
	}
}
