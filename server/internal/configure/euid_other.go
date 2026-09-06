//go:build !unix

package configure

func effectiveUID() int { return -1 }
