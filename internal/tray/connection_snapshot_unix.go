//go:build darwin || linux

package tray

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockConnectionSnapshot(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !snapshotOwnedByUser(info) {
		_ = file.Close()
		return nil, errors.New("snapshot must be an owned regular file")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func unlockConnectionSnapshot(file *os.File) error {
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}

func snapshotOwnedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
