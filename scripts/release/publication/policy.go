package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var stableTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validatePolicy(p doc) error {
	if field(p, "schema_version") != "spine-cli.release-policy/v1" {
		return errors.New("지원하지 않는 릴리스 정책입니다")
	}
	next := field(p, "next_version")
	if !stableTag.MatchString(next) {
		return errors.New("next_version에는 정식 버전을 지정해야 합니다")
	}
	for _, key := range []string{"preview_hours", "rc_hours", "stable_interval_hours"} {
		value, _ := p[key].(json.Number)
		hours, err := value.Int64()
		if key == "stable_interval_hours" {
			if err != nil || hours < 0 {
				return errors.New("정식 출시 간격은 0 이상의 시간이어야 합니다")
			}
		} else if err != nil || hours <= 0 {
			return fmt.Errorf("%s에는 양의 시간을 지정해야 합니다", key)
		}
	}
	if field(p, "approval_environment") != "release-stable" || field(p, "reviewer_login") == "" {
		return errors.New("정식 승인 환경과 지정 리뷰어가 필요합니다")
	}
	if !reflect.DeepEqual(p["branches"], doc{"preview": "preview", "rc": "rc", "stable": "main"}) {
		return errors.New("출시 브랜치는 preview → rc → main으로 지정해야 합니다")
	}
	if !reflect.DeepEqual(p["required_public_os"], []any{"linux", "darwin", "windows"}) {
		return errors.New("공개 설치는 Linux, macOS, Windows에서 검증해야 합니다")
	}
	recipe := object(p, "recipe")
	pins := object(recipe, "modules")
	if _, ok := pins[core]; field(recipe, "id") == "" || !ok {
		return errors.New("기본 생성 레시피와 모듈 버전을 고정해야 합니다")
	}
	for path, raw := range pins {
		version, _ := raw.(string)
		if version == "preview" && field(recipe, "id") == "platform-contracts-preview" && strings.HasPrefix(path, core) {
			continue
		}
		if !fixedVersion.MatchString(version) {
			return errors.New("Preview 레시피 외에는 고정 모듈 버전을 지정해야 합니다")
		}
	}
	if field(p, "notes_path") != "release/notes/"+next+".md" {
		return errors.New("대상 버전의 릴리스 초안 경로가 필요합니다")
	}
	return nil
}
