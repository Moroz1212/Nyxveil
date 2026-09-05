package windows_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/nyxveil/nvp/core/platform/windows"
	"github.com/nyxveil/nvp/core/tunnel"
)

type mockTUN struct {
	mtu  int
	name string
}

func (m *mockTUN) Read(p []byte) (int, error)  { return 0, io.EOF }
func (m *mockTUN) Write(p []byte) (int, error) { return len(p), nil }
func (m *mockTUN) Close() error                { return nil }
func (m *mockTUN) MTU() int                    { return m.mtu }
func (m *mockTUN) Name() string                { return m.name }

func TestWindowsTUNRequiresBinding(t *testing.T) {
	f := windows.NewFactory()
	_, err := f.Open(context.Background(), tunnel.Config{Name: "Nyxveil", MTU: 1280})
	if !errors.Is(err, windows.ErrWintunNotLinked) {
		t.Fatalf("expected ErrWintunNotLinked, got %v", err)
	}
}

func TestWindowsTUNInjectedBinding(t *testing.T) {
	f := windows.NewFactory()
	f.OpenFunc = func(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
		return &mockTUN{mtu: cfg.MTU, name: cfg.Name}, nil
	}
	dev, err := f.Open(context.Background(), tunnel.Config{Name: "t", MTU: 1280})
	if err != nil {
		t.Fatal(err)
	}
	if dev.Name() != "t" || dev.MTU() != 1280 {
		t.Fatalf("unexpected device %+v", dev)
	}
}
