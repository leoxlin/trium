package reconcile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireLock(ctx context.Context, path string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reason, err := checkPath(path)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, fmt.Errorf("lock %s: %s", path, reason)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire apply lock %s: %w", path, err)
	}
	// Keep the lock inode: removing it permits another process to lock a different file.
	unlock := func() error { return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close()) }
	if err := ctx.Err(); err != nil {
		_ = unlock()
		return nil, err
	}
	return unlock, nil
}
