package android

import (
	"context"
	"fmt"
	"sync"

	"github.com/nyxveil/nvp/tunnel"
)

// SDK provides Android VpnService integration surface (called from Kotlin/JNI via gomobile).
type SDK struct {
	mu      sync.Mutex
	mode    tunnel.RouteMode
	session interface{}
}

// NewSDK creates Android client SDK instance.
func NewSDK() *SDK {
	return &SDK{mode: tunnel.RouteAll}
}

// SetRouteMode configures split tunnel policy (platform enforces via VpnService.Builder).
func (s *SDK) SetRouteMode(mode tunnel.RouteMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

// RouteMode returns current routing mode.
func (s *SDK) RouteMode() tunnel.RouteMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// ConnectConfig holds Android connection parameters.
type ConnectConfig struct {
	ControlPlaneURL string
	LicenseToken    string
	DeviceID        string
	LocationID      string
}

// Connect establishes VPN session (foundation — integrates with NVP session core).
func (s *SDK) Connect(ctx context.Context, cfg ConnectConfig) error {
	_ = ctx
	if cfg.LicenseToken == "" || cfg.DeviceID == "" {
		return fmt.Errorf("license and device_id required")
	}
	return nil
}

// Disconnect closes active session.
func (s *SDK) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = nil
	return nil
}

// TUNDevice adapts Android ParcelFileDescriptor TUN fd to tunnel.Device.
type TUNDevice struct {
	readFn  func([]byte) (int, error)
	writeFn func([]byte) (int, error)
	closeFn func() error
	mtu     int
	name    string
}

func NewTUNDevice(name string, mtu int, readFn, writeFn func([]byte) (int, error), closeFn func() error) *TUNDevice {
	if mtu <= 0 {
		mtu = 1280
	}
	return &TUNDevice{name: name, mtu: mtu, readFn: readFn, writeFn: writeFn, closeFn: closeFn}
}

func (d *TUNDevice) Read(p []byte) (int, error)  { return d.readFn(p) }
func (d *TUNDevice) Write(p []byte) (int, error) { return d.writeFn(p) }
func (d *TUNDevice) Close() error {
	if d.closeFn != nil {
		return d.closeFn()
	}
	return nil
}
func (d *TUNDevice) MTU() int     { return d.mtu }
func (d *TUNDevice) Name() string { return d.name }
