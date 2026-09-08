package nyxveilbridge

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/nyxveil/nvp/core/tunnel"
)

// tunFile wraps an Android VpnService TUN fd (raw IP packets, not Ethernet).
type tunFile struct {
	f   *os.File
	mtu int
	fd  int
}

var _ tunnel.Device = (*tunFile)(nil)

func newTunFile(tunFd int, mtu int) (*tunFile, error) {
	if tunFd < 0 {
		return nil, errors.New("invalid tun fd")
	}
	// Android VpnService FD defaults to non-blocking. Go blocking Read expects blocking mode;
	// EAGAIN would otherwise kill the TX pump immediately (TX/RX stay at 0).
	setFDBlocking(tunFd)
	f := os.NewFile(uintptr(tunFd), "nyxveil-tun")
	if f == nil {
		_ = closeFD(tunFd)
		return nil, errors.New("NewFile failed")
	}
	return &tunFile{f: f, mtu: mtu, fd: tunFd}, nil
}

func (t *tunFile) Read(p []byte) (int, error) {
	for {
		n, err := t.f.Read(p)
		if err == nil {
			return n, nil
		}
		if isAgain(err) {
			// Recoverable if FD somehow remains non-blocking — never fatal, never busy-spin.
			time.Sleep(2 * time.Millisecond)
			continue
		}
		return n, err
	}
}

func (t *tunFile) Write(p []byte) (int, error) {
	off := 0
	for off < len(p) {
		n, err := t.f.Write(p[off:])
		if n > 0 {
			off += n
		}
		if err == nil {
			return off, nil
		}
		if isAgain(err) {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		return off, err
	}
	return off, nil
}

func (t *tunFile) Close() error {
	if t.f == nil {
		return nil
	}
	err := t.f.Close()
	t.f = nil
	return err
}

func (t *tunFile) MTU() int     { return t.mtu }
func (t *tunFile) Name() string { return "nyxveil0" }

func isAgain(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EAGAIN || errno == syscall.EWOULDBLOCK
	}
	return false
}

func isTunClosed(err error) bool {
	return err != nil && (errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, syscall.EBADF))
}

func closeFD(fd int) error {
	if fd < 0 {
		return nil
	}
	// Portable across Windows host tests and Android: NewFile takes ownership and Close.
	return os.NewFile(uintptr(fd), "close-fd").Close()
}
