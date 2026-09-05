//go:build linux

package tun

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	defaultName = "nyxveil0"
	tunPath     = "/dev/net/tun"
	iffTun      = 0x0001
	iffNoPi     = 0x1000
)

// Device is a Linux TUN interface (IFF_TUN | IFF_NO_PI).
type Device struct {
	f    *os.File
	name string
	mtu  int
}

type ifReq struct {
	Name  [unix.IFNAMSIZ]byte
	Flags uint16
	pad   [22]byte
}

// Open creates/attaches the named TUN device and configures address + MTU via ip(8).
// addrCIDR must be host/prefix (e.g. 10.66.0.1/24). name defaults to nyxveil0.
func Open(name, addrCIDR string, mtu int) (*Device, error) {
	if name == "" {
		name = defaultName
	}
	if mtu <= 0 {
		mtu = 1420
	}
	f, err := os.OpenFile(tunPath, os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("tun: open %s: %w", tunPath, err)
	}
	var req ifReq
	copy(req.Name[:], name)
	req.Flags = iffTun | iffNoPi
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("tun: TUNSETIFF: %w", errno)
	}
	actual := strings.TrimRight(string(req.Name[:]), "\x00")
	d := &Device{f: f, name: actual, mtu: mtu}
	if err := d.configure(addrCIDR, mtu); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Device) configure(addrCIDR string, mtu int) error {
	// Prefer ip(8) for address/MTU — available on all supported Linux node images.
	if out, err := exec.Command("ip", "link", "set", "dev", d.name, "mtu", strconv.Itoa(mtu), "up").CombinedOutput(); err != nil {
		return fmt.Errorf("tun: ip link set: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// Replace any existing address on the device for idempotent restarts.
	_ = exec.Command("ip", "addr", "flush", "dev", d.name).Run()
	if out, err := exec.Command("ip", "addr", "add", addrCIDR, "dev", d.name).CombinedOutput(); err != nil {
		return fmt.Errorf("tun: ip addr add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Name returns the interface name.
func (d *Device) Name() string { return d.name }

// MTU returns the configured MTU.
func (d *Device) MTU() int { return d.mtu }

// Read reads one IP packet from the TUN device.
func (d *Device) Read(p []byte) (int, error) {
	return d.f.Read(p)
}

// Write writes one IP packet to the TUN device.
func (d *Device) Write(p []byte) (int, error) {
	return d.f.Write(p)
}

// Close closes the TUN file descriptor.
func (d *Device) Close() error {
	if d == nil || d.f == nil {
		return nil
	}
	return d.f.Close()
}
