package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// 업무 소스와 embed 자산을 함께 복사하고, 같은 입력을 게시 직전에 다시 확인합니다.
// staging이 비어 있으면 파일을 쓰지 않고 현재 입력의 해시만 계산합니다.
func snapshotUpdateProject(ctx context.Context, root, staging string) (string, error) {
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.Dir(relative) == "." {
				switch relative {
				case ".git", "bin", ".spine", ".gocache", ".gomodcache", "node_modules":
					return filepath.SkipDir
				}
			}
			if staging != "" {
				return os.MkdirAll(filepath.Join(staging, relative), 0o755)
			}
			return nil
		}
		if relative == generationLockPath || relative == ".git" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("update input is not a regular file: %s; symlinks and special files are outside the snapshot recipe", relative)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		fmt.Fprintf(digest, "%d:%s:%o\n%d:", len(name), name, info.Mode().Perm(), len(contents))
		digest.Write(contents)
		if staging != "" {
			// 의존성 명령은 임시 go.mod와 go.sum만 수정할 수 있도록 쓰기 권한을 더합니다.
			mode := info.Mode().Perm()
			if relative == "go.mod" || relative == "go.sum" {
				mode |= 0o200
			}
			return os.WriteFile(filepath.Join(staging, relative), contents, mode)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	// 등록 조건에 사용하는 os.Getenv 입력도 기존 생성 계획과 같은 기준으로 고정합니다.
	_, sourceHash, err := readLayoutSources()
	if err != nil {
		return "", err
	}
	fmt.Fprintln(digest, sourceHash)
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
