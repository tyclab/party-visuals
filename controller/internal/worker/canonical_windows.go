//go:build windows

package worker

import (
	"strings"

	"golang.org/x/sys/windows"
)

// resolve asks Windows for the final path of an existing file or folder:
// FILE_NAME_NORMALIZED with VOLUME_NAME_DOS, both 0.
func resolve(path string) (string, bool) {
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return "", false
	}
	h, e := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if e != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, e := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if e != nil {
			return "", false
		}
		if n < uint32(len(buf)) {
			final := windows.UTF16ToString(buf[:n])
			if strings.HasPrefix(final, `\\?\UNC\`) {
				return `\\` + final[len(`\\?\UNC\`):], true
			}
			return strings.TrimPrefix(final, `\\?\`), true
		}
		buf = make([]uint16, n)
	}
}
