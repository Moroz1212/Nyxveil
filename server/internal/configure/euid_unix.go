//go:build unix

package configure

import "os"

func effectiveUID() int { return os.Geteuid() }
