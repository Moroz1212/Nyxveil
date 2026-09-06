//go:build windows

package wintundev

import (
	"context"
	"fmt"
	"io"
	"sync"

	nvpwin "github.com/nyxveil/nvp/core/platform/windows"
	"github.com/nyxveil/nvp/core/tunnel"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// Open creates a Wintun adapter implementing tunnel.Device.
// Requires wintun.dll beside the service binary (WireGuard redistributable).
// Missing/unloadable DLL → ErrWintunNotLinked (client maps to engine.ErrNotLinked).
func Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	name := cfg.Name
	if name == "" {
		name = "Nyxveil"
	}
	mtu := cfg.MTU
	if mtu <= 0 {
		mtu = 1420
	}
	adapter, err := wintun.CreateAdapter(name, "Wintun", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: CreateAdapter: %v", nvpwin.ErrWintunNotLinked, err)
	}
	session, err := adapter.StartSession(0x800000) // 8 MiB ring
	if err != nil {
		_ = adapter.Close()
		return nil, fmt.Errorf("wintun: StartSession: %w", err)
	}
	d := &device{
		adapter: adapter,
		session: session,
		name:    name,
		mtu:     mtu,
		luid:    adapter.LUID(),
		readCh:  make(chan []byte, 64),
		errCh:   make(chan error, 1),
	}
	d.wg.Add(1)
	go d.receiveLoop()
	return d, nil
}

type device struct {
	adapter *wintun.Adapter
	session wintun.Session
	name    string
	mtu     int
	luid    uint64

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	readCh chan []byte
	errCh  chan error
	rbuf   []byte
}

func (d *device) receiveLoop() {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		closed := d.closed
		sess := d.session
		d.mu.Unlock()
		if closed {
			return
		}
		packet, err := sess.ReceivePacket()
		if err != nil {
			if err == windows.ERROR_NO_MORE_ITEMS {
				windows.WaitForSingleObject(sess.ReadWaitEvent(), windows.INFINITE)
				continue
			}
			select {
			case d.errCh <- err:
			default:
			}
			return
		}
		cp := make([]byte, len(packet))
		copy(cp, packet)
		sess.ReleaseReceivePacket(packet)
		select {
		case d.readCh <- cp:
		default:
			// drop if reader too slow
		}
	}
}

func (d *device) Read(p []byte) (int, error) {
	if len(d.rbuf) > 0 {
		n := copy(p, d.rbuf)
		d.rbuf = d.rbuf[n:]
		return n, nil
	}
	select {
	case err := <-d.errCh:
		if err == nil {
			return 0, io.EOF
		}
		return 0, err
	case pkt, ok := <-d.readCh:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, pkt)
		if n < len(pkt) {
			d.rbuf = pkt[n:]
		}
		return n, nil
	}
}

func (d *device) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, io.ErrClosedPipe
	}
	pkt, err := d.session.AllocateSendPacket(len(p))
	if err != nil {
		return 0, err
	}
	copy(pkt, p)
	d.session.SendPacket(pkt)
	return len(p), nil
}

func (d *device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	sess := d.session
	ad := d.adapter
	d.mu.Unlock()
	sess.End()
	d.wg.Wait()
	close(d.readCh)
	return ad.Close()
}

func (d *device) MTU() int    { return d.mtu }
func (d *device) Name() string { return d.name }

// AdapterLUID returns the Wintun LUID for IP Helper configuration.
func AdapterLUID(dev tunnel.Device) (uint64, bool) {
	d, ok := dev.(*device)
	if !ok {
		return 0, false
	}
	return d.luid, true
}
