package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type lockInfo struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

func AcquireLock(ctx context.Context, path string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	for attempts := 0; attempts < 2; attempts++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			stale, checkErr := staleLock(path)
			if checkErr != nil {
				return nil, checkErr
			}
			if !stale {
				return nil, fmt.Errorf("another Skillet apply holds %s", path)
			}
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("remove stale lock %s: %w", path, err)
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("acquire lock %s: %w", path, err)
		}
		info := lockInfo{PID: os.Getpid(), StartedAt: time.Now().UTC()}
		encodeErr := json.NewEncoder(file).Encode(info)
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			_ = os.Remove(path)
			return nil, fmt.Errorf("write lock %s: %w", path, errors.Join(encodeErr, closeErr))
		}
		return func() error {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("release lock %s: %w", path, err)
			}
			return nil
		}, nil
	}
	return nil, fmt.Errorf("could not acquire lock %s", path)
}

func staleLock(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read lock %s: %w", path, err)
	}
	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil || info.PID <= 0 {
		return false, fmt.Errorf("invalid lock file %s", path)
	}
	err = syscall.Kill(info.PID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	if err == nil || errors.Is(err, syscall.EPERM) {
		return false, nil
	}
	return false, fmt.Errorf("inspect lock holder %s: %w", path, err)
}

func LockPath(home string) string {
	return filepath.Join(home, ".skillet", "apply.lock")
}

func BackupRoot(home string) string {
	return filepath.Join(home, ".skillet", "backups")
}
