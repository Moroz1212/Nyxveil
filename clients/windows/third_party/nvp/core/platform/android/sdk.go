package android

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nyxveil/nvp/core/connector"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/tunnel"
)

// ErrPlatformIncomplete is returned when Connect is called without a full
// VpnService TUN + transport registry wiring.
var ErrPlatformIncomplete = errors.New("android production VPN requires VpnService TUN and connector.OpenSession wiring")

// SDK provides Android VpnService integration surface (gomobile-compatible).
type SDK struct {
	mu        sync.Mutex
	mode      tunnel.RouteMode
	connector *connector.Connector
	session   *session.Session
	cpURL     string
}

func NewSDK() *SDK {
	return &SDK{mode: tunnel.RouteAll}
}

func (s *SDK) SetRouteMode(mode tunnel.RouteMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

func (s *SDK) RouteMode() tunnel.RouteMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// ConnectConfig holds parameters for a full VPN session via Connector.
type ConnectConfig struct {
	ControlPlaneURL string
	LicenseToken    string
	DeviceID        string
	LocationID      string
	Connector       *connector.Connector
	SessionConfig   connector.ConnectConfig
}

// Connect establishes a real NVP session using Connector.OpenSession.
// It does not create a TUN by itself — platform code must attach TUNDevice separately.
func (s *SDK) Connect(ctx context.Context, cfg ConnectConfig) error {
	if cfg.Connector == nil {
		return fmt.Errorf("%w: Connector required", ErrPlatformIncomplete)
	}
	if cfg.LicenseToken == "" || cfg.DeviceID == "" {
		return fmt.Errorf("license and device_id required")
	}
	sessCfg := cfg.SessionConfig
	if sessCfg.LicenseToken == "" {
		sessCfg.LicenseToken = cfg.LicenseToken
	}
	if sessCfg.DeviceID == "" {
		sessCfg.DeviceID = cfg.DeviceID
	}
	if sessCfg.LocationID == "" {
		sessCfg.LocationID = cfg.LocationID
	}
	sess, conn, _, err := cfg.Connector.OpenSession(ctx, sessCfg)
	if err != nil {
		return err
	}
	_ = conn
	s.mu.Lock()
	s.connector = cfg.Connector
	s.session = sess
	s.cpURL = cfg.ControlPlaneURL
	s.mu.Unlock()
	return nil
}

func (s *SDK) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		_ = s.session.Close(context.Background())
		s.session = nil
	}
	s.connector = nil
	return nil
}

func (s *SDK) Session() *session.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func (s *SDK) ControlPlaneURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cpURL
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

// Ensure transport import used for API docs / compile of dial types.
var _ = transport.ProfileTLSTCP
