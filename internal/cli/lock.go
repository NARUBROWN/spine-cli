package cli

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	generationLockPath = ".spine-generate.lock"
	generationLockTTL  = 10 * time.Minute
)

type projectLock struct {
	path  string
	owner string
}

// acquireProjectLock은 여러 CLI 프로세스가 같은 composition root를 덮어쓰는 경쟁을 막습니다.
func acquireProjectLock(path string) (projectLock, error) {
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			owner := fmt.Sprintf("pid=%d\ncreated_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			if _, writeErr := file.WriteString(owner); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return projectLock{}, writeErr
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return projectLock{}, closeErr
			}
			return projectLock{path: path, owner: owner}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			// Windows can report an existing directory as "is a directory"
			// instead of os.ErrExist. Normalize every existing non-regular
			// target to the same safe error on every supported platform.
			if info, statErr := os.Lstat(path); statErr == nil && !info.Mode().IsRegular() {
				return projectLock{}, fmt.Errorf("generation lock %s is not a regular file; inspect it before continuing", path)
			}
			return projectLock{}, err
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return projectLock{}, statErr
		}
		if !info.Mode().IsRegular() {
			return projectLock{}, fmt.Errorf("generation lock %s is not a regular file; inspect it before continuing", path)
		}
		if time.Since(info.ModTime()) <= generationLockTTL {
			return projectLock{}, fmt.Errorf("another Spine generation is in progress (%s); retry after it finishes", path)
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return projectLock{}, fmt.Errorf("could not clear stale generation lock %s: %w", path, removeErr)
		}
	}
	return projectLock{}, fmt.Errorf("could not acquire generation lock %s", path)
}

func (l projectLock) release() {
	if l.path == "" {
		return
	}
	contents, err := os.ReadFile(l.path)
	if err == nil && string(contents) == l.owner {
		_ = os.Remove(l.path)
	}
}
