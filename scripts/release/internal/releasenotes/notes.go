package releasenotes

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/NARUBROWN/spine-cli/scripts/release/internal/reports"
)

func Body(plan reports.Document, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.TrimRightFunc(content, unicode.IsSpace)
	if !utf8.Valid(data) || content == "" || strings.ContainsRune(content, '\u00b7') {
		return "", errors.New("릴리스 초안의 문장과 내용을 확인하세요")
	}
	components, _ := plan["components"].(map[string]any)
	version, _ := plan["version"].(string)
	channel, _ := plan["channel"].(string)
	source, _ := plan["source_sha"].(string)
	recipe, _ := components["recipe_sha256"].(string)
	stage, _ := plan["source_tag"].(string)
	reason, _ := plan["observation_override_reason"].(string)
	if version == "" || channel == "" || source == "" || recipe == "" {
		return "", errors.New("릴리스 초안에 필요한 발행 정보가 없습니다")
	}
	if stage == "" {
		stage = "없음"
	}
	if reason == "" {
		reason = "없음"
	}
	return fmt.Sprintf("%s\n\n버전: `%s`\n\n채널: `%s`\n\n소스: `%s`\n\n이전 단계: `%s`\n\n레시피 SHA-256: `%s`\n\n관찰 기간 단축 사유: %s\n\n상세 검증 결과는 release-manifest.json을 확인하세요.\n", content, version, channel, source, stage, recipe, reason), nil
}

func WriteReview(plan reports.Document, notesPath, directory string) error {
	body, err := Body(plan, notesPath)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != plan["notes_sha256"] {
		return errors.New("검토한 발행 본문이 바뀌었습니다")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "release-title.txt"), []byte(plan["version"].(string)+"\n"), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "release-notes.md"), []byte(body), 0644)
}
