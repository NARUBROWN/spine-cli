package reports

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
)

type Document = map[string]any

func Decode(data []byte) (Document, error) {
	var doc Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !json.Valid(data) {
		return nil, errors.New("invalid JSON")
	}
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("expected a JSON object")
	}
	return doc, nil
}

func truthy(v any) bool {
	switch value := v.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case Document:
		return len(value) > 0
	case []any:
		return len(value) > 0
	case json.Number:
		n, _ := value.Float64()
		return n != 0
	default:
		return true
	}
}

func stringsFrom(v any) ([]string, bool) {
	if list, ok := v.([]string); ok {
		return slices.Clone(list), true
	}
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func Validate(all []Document, plan Document, query string) error {
	scope := plan["public_validation_scope"]
	if scope == "tag-install" && (query != plan["version"] || !truthy(plan["source_evidence"])) {
		return errors.New("태그 설치 검증에는 승격 근거와 정확한 새 태그가 필요합니다")
	}
	required, ok := stringsFrom(plan["required_public_os"])
	if !ok {
		return errors.New("공개 설치 결과의 OS가 누락되거나 중복됐습니다")
	}
	found := make([]string, 0, len(all))
	for _, report := range all {
		name, _ := report["os"].(string)
		found = append(found, name)
	}
	slices.Sort(found)
	slices.Sort(required)
	if !slices.Equal(found, required) {
		return errors.New("공개 설치 결과의 OS가 누락되거나 중복됐습니다")
	}
	for _, report := range all {
		entries, _ := stringsFrom(report["entrypoints"])
		slices.Sort(entries)
		if report["result"] != "passed" || !reflect.DeepEqual(report["source_sha"], plan["source_sha"]) ||
			report["query"] != query || !reflect.DeepEqual(report["components"], plan["components"]) ||
			!reflect.DeepEqual(report["scope"], scope) || !slices.Equal(slices.Compact(entries), []string{"spine", "spine-cli"}) {
			return errors.New("공개 설치 결과가 발행 계획과 다릅니다")
		}
		if scope == "tag-install" {
			if report["resolved_cli_version"] != query {
				return errors.New("공개 설치가 발행한 태그를 확인하지 못했습니다")
			}
			continue
		}
		for _, check := range []struct{ evidence, checks, message string }{
			{"upstream_evidence", "combination_checks", "공개 설치 결과에 코어와 플랫폼의 조합 검증이 없습니다"},
			{"rc_cohort", "rc_combination_checks", "RC 공개 설치 결과에 전체 RC 조합 검증이 없습니다"},
		} {
			if plan[check.evidence] == nil {
				continue
			}
			cases, _ := stringsFrom(report[check.checks])
			if !reflect.DeepEqual(report[check.evidence], plan[check.evidence]) || !slices.Equal(cases, []string{"echo-full", "nethttp-full"}) {
				return errors.New(check.message)
			}
		}
	}
	return nil
}

func Collect(directory string, plan Document, query string) ([]Document, error) {
	all := make([]Document, 0)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == directory && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || entry.Name() != "release-validation.json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := Decode(raw)
		if err != nil {
			return err
		}
		all = append(all, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := Validate(all, plan, query); err != nil {
		return nil, err
	}
	return all, nil
}
