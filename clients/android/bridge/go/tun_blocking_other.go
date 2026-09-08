//go:build !unix

package nyxveilbridge

func setFDBlocking(fd int) {
	// Host unit tests on Windows: no-op (Android builds use unix SetNonblock).
	_ = fd
}
