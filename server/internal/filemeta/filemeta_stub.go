//go:build !unix

package filemeta

import "os"

func ownerFromFileInfo(fi os.FileInfo) (uid, gid int, ok bool) {
	return -1, -1, false
}

func chownPath(name string, uid, gid int) error {
	return nil
}
