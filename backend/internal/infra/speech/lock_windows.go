//go:build windows

package speech

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockStore(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
