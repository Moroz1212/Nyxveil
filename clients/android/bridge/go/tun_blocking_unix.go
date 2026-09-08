//go:build unix

package nyxveilbridge

import "golang.org/x/sys/unix"

func setFDBlocking(fd int) {
	_ = unix.SetNonblock(fd, false)
}
