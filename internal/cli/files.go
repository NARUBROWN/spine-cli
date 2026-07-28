package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type writeOptions struct {
	DryRun bool
	Force  bool
}

type fileChange struct {
	Path           string
	Contents       string
	AllowOverwrite bool
}

type fileBackup struct {
	contents []byte
	mode     os.FileMode
	existed  bool
}

func renderFileChanges(changes []fileChange) (string, error) {
	changes = append([]fileChange(nil), changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	var result strings.Builder
	for _, change := range changes {
		previous, err := os.ReadFile(change.Path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if string(previous) == change.Contents {
			continue
		}
		oldPath := change.Path
		oldLines := splitDiffLines(string(previous))
		if os.IsNotExist(err) {
			oldPath = "/dev/null"
			oldLines = nil
		}
		newLines := splitDiffLines(change.Contents)
		fmt.Fprintf(&result, "--- %s\n+++ %s\n", oldPath, change.Path)
		fmt.Fprintf(&result, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
		for _, line := range oldLines {
			fmt.Fprintf(&result, "-%s\n", line)
		}
		for _, line := range newLines {
			fmt.Fprintf(&result, "+%s\n", line)
		}
	}
	return result.String(), nil
}

func splitDiffLines(contents string) []string {
	if contents == "" {
		return nil
	}
	contents = strings.TrimSuffix(contents, "\n")
	if contents == "" {
		return []string{""}
	}
	return strings.Split(contents, "\n")
}

// applyFileChanges는 모든 대상을 사전 검사한 다음 생성 계획 전체를 적용합니다.
// 각 파일은 대상과 같은 디렉터리에 준비하며, 반영 도중 실패하면 이미 교체한 파일을 복원합니다.
func applyFileChanges(changes []fileChange, dryRun bool) error {
	if len(changes) == 0 {
		return nil
	}
	changes = append([]fileChange(nil), changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	backups := make(map[string]fileBackup, len(changes))
	for i, change := range changes {
		if change.Path == "" {
			return fmt.Errorf("generation plan contains an empty path")
		}
		if i > 0 && changes[i-1].Path == change.Path {
			return fmt.Errorf("generation plan contains duplicate target: %s", change.Path)
		}
		info, err := os.Stat(change.Path)
		switch {
		case err == nil:
			if info.IsDir() {
				return fmt.Errorf("generation target is a directory: %s", change.Path)
			}
			if !change.AllowOverwrite {
				return fmt.Errorf("refusing to overwrite existing file: %s (use --force to replace it)", change.Path)
			}
			contents, readErr := os.ReadFile(change.Path)
			if readErr != nil {
				return readErr
			}
			backups[change.Path] = fileBackup{contents: contents, mode: info.Mode(), existed: true}
		case os.IsNotExist(err):
			backups[change.Path] = fileBackup{mode: 0o644}
		default:
			return err
		}
	}
	if dryRun {
		return nil
	}

	temps := make(map[string]string, len(changes))
	cleanupTemps := func() {
		for _, path := range temps {
			_ = os.Remove(path)
		}
	}
	for _, change := range changes {
		dir := filepath.Dir(change.Path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			cleanupTemps()
			return err
		}
		tmp, err := os.CreateTemp(dir, ".spine-stage-*")
		if err != nil {
			cleanupTemps()
			return err
		}
		tmpPath := tmp.Name()
		temps[change.Path] = tmpPath
		mode := backups[change.Path].mode
		if mode == 0 {
			mode = 0o644
		}
		if err := tmp.Chmod(mode.Perm()); err != nil {
			_ = tmp.Close()
			cleanupTemps()
			return err
		}
		if _, err := tmp.WriteString(change.Contents); err != nil {
			_ = tmp.Close()
			cleanupTemps()
			return err
		}
		if err := tmp.Close(); err != nil {
			cleanupTemps()
			return err
		}
	}

	var committed []string
	for _, change := range changes {
		if err := os.Rename(temps[change.Path], change.Path); err != nil {
			rollbackErr := rollbackFileChanges(committed, backups)
			cleanupTemps()
			if rollbackErr != nil {
				return fmt.Errorf("could not commit generation plan at %s: %w; rollback also failed: %v", change.Path, err, rollbackErr)
			}
			return fmt.Errorf("could not commit generation plan at %s: %w", change.Path, err)
		}
		delete(temps, change.Path)
		committed = append(committed, change.Path)
	}
	return nil
}

func rollbackFileChanges(paths []string, backups map[string]fileBackup) error {
	var failures []string
	for i := len(paths) - 1; i >= 0; i-- {
		path := paths[i]
		backup := backups[path]
		if !backup.existed {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				failures = append(failures, fmt.Sprintf("remove %s: %v", path, err))
			}
			continue
		}
		dir := filepath.Dir(path)
		tmp, err := os.CreateTemp(dir, ".spine-rollback-*")
		if err != nil {
			failures = append(failures, fmt.Sprintf("stage %s: %v", path, err))
			continue
		}
		tmpPath := tmp.Name()
		if err := tmp.Chmod(backup.mode.Perm()); err == nil {
			_, err = tmp.Write(backup.contents)
		}
		closeErr := tmp.Close()
		if err == nil && closeErr == nil {
			if renameErr := os.Rename(tmpPath, path); renameErr != nil {
				failures = append(failures, fmt.Sprintf("restore %s: %v", path, renameErr))
				_ = os.Remove(tmpPath)
			}
		} else {
			_ = os.Remove(tmpPath)
			if err != nil {
				failures = append(failures, fmt.Sprintf("write backup %s: %v", path, err))
			} else {
				failures = append(failures, fmt.Sprintf("close backup %s: %v", path, closeErr))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}
