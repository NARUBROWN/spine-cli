package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxCloudErrorBytes = 16 << 10

// 관리 API의 표시용 메시지만 사용하고 터미널 제어 문자와 지나치게 긴 응답은 거부한다.
func safeCloudMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" || len(message) > 1024 || !utf8.ValidString(message) {
		return ""
	}
	for _, r := range message {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ""
		}
	}
	return message
}

type cloudAPIError struct {
	status  int
	code    string
	message string
}

func (e *cloudAPIError) Error() string { return e.message }
func (e *cloudAPIError) Is(target error) bool {
	return target == errCloudAppNameExists && e.status == http.StatusConflict && e.code == "APP_NAME_EXISTS"
}

func cloudResponseError(res *http.Response, fallback string) error {
	data, err := io.ReadAll(io.LimitReader(res.Body, maxCloudErrorBytes+1))
	if err != nil || len(data) > maxCloudErrorBytes {
		return errors.New(fallback)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return errors.New(fallback)
	}
	message := safeCloudMessage(payload.Message)
	if message == "" {
		message = safeCloudMessage(payload.Error)
	}
	if message == "" {
		return errors.New(fallback)
	}
	code := payload.Code
	// 이전 API는 코드 없이 오류 문자열만 제공한다.
	if code == "" && res.StatusCode == http.StatusConflict && message == "app name already exists" {
		code = "APP_NAME_EXISTS"
	}
	return &cloudAPIError{status: res.StatusCode, code: code, message: message}
}
