//go:build unix

package filemeta

import (
	"os"
	"syscall"
)

func ownerFromFileInfo(fi os.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return -1, -1, false
	}
	return int(st.Uid), int(st.Gid), true
}

func chownPath(name string, uid, gid int) error {
	return os.Chown(name, uid, gid)
}
