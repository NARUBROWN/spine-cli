package cli

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxCloudArtifactBytes int64 = 32 << 20

type cloudArtifact struct {
	Binary       []byte
	SHA256       string
	OS           string
	Architecture string
}

// buildCloudArtifact는 계약을 확인하고 OCI 대상 정적 실행 파일을 임시로 생성한다.
// 업로드와 실행은 별도 단계이며 이 함수의 성공은 배포 성공을 뜻하지 않는다.
func buildCloudArtifact(ctx context.Context, out, errOut io.Writer) (cloudArtifact, error) {
	var empty cloudArtifact
	if code := runContractCheck(nil, out, errOut); code != 0 {
		return empty, errors.New("Spine contract validation failed")
	}
	config, err := loadProjectConfig()
	if err != nil {
		return empty, err
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return empty, errors.New("could not resolve project directory")
	}
	frontend, err := resolveFrontendSettings(root, config)
	if err != nil {
		return empty, err
	}
	var overlay *frontendOverlay
	if frontend != nil {
		dir, err := os.MkdirTemp("", "spine-cloud-frontend-")
		if err != nil {
			return empty, fmt.Errorf("could not create temporary frontend build directory: %w", err)
		}
		defer os.RemoveAll(dir)
		frontend.output = filepath.Join(dir, "frontend")
		builder, err := newFrontendBuilderForSettings(root, *frontend, true)
		if err != nil {
			return empty, fmt.Errorf("could not initialize frontend build: %w", err)
		}
		defer builder.close()
		if err := builder.rebuild(errOut); err != nil {
			return empty, fmt.Errorf("frontend build failed: %w", err)
		}
		overlay, err = createFrontendServingOverlay(root, config, frontend, true)
		if err != nil {
			return empty, fmt.Errorf("could not prepare embedded frontend: %w", err)
		}
		defer overlay.cleanup()
	}
	return compileCloudArtifact(ctx, config.Entrypoint, maxCloudArtifactBytes, errOut, overlay)
}

func compileCloudArtifact(ctx context.Context, entrypoint string, limit int64, errOut io.Writer, overlay *frontendOverlay) (cloudArtifact, error) {
	var empty cloudArtifact
	if entrypoint == "" || limit <= 0 {
		return empty, errors.New("invalid artifact build configuration")
	}
	dir, err := os.MkdirTemp("", "spine-cloud-build-*")
	if err != nil {
		return empty, errors.New("could not create temporary build directory")
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "app")
	args := []string{"build", "-trimpath"}
	if overlay != nil {
		args = append(args, "-overlay", overlay.path)
	}
	cmd := exec.CommandContext(ctx, "go", append(args, "-o", output, entrypoint)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GOOS=") && !strings.HasPrefix(value, "GOARCH=") && !strings.HasPrefix(value, "CGO_ENABLED=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	cmd.Stdout = errOut
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return empty, errors.New("Linux ARM64 build failed")
	}
	info, err := os.Stat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return empty, errors.New("build artifact is missing or exceeds the size limit")
	}
	binary, err := elf.Open(output)
	if err != nil {
		return empty, errors.New("build artifact is not a valid ELF executable")
	}
	valid := binary.Class == elf.ELFCLASS64 && binary.Machine == elf.EM_AARCH64 && (binary.Type == elf.ET_EXEC || binary.Type == elf.ET_DYN)
	for _, p := range binary.Progs {
		if p.Type == elf.PT_INTERP {
			valid = false
		}
	}
	_ = binary.Close()
	if !valid {
		return empty, errors.New("build artifact must be a static Linux ARM64 executable")
	}
	data, err := os.ReadFile(output)
	if err != nil || int64(len(data)) > limit {
		return empty, errors.New("could not read build artifact")
	}
	sum := sha256.Sum256(data)
	return cloudArtifact{Binary: data, SHA256: hex.EncodeToString(sum[:]), OS: "linux", Architecture: "arm64"}, nil
}

func artifactSummary(a cloudArtifact) string {
	return fmt.Sprintf("%s/%s, %d bytes, sha256 %s", a.OS, a.Architecture, len(a.Binary), a.SHA256)
}
