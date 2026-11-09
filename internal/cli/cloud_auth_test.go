package cli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

type memoryCloudStore struct {
	value string
	err   error
}

func (s *memoryCloudStore) Set(service, account, value string) error {
	if service != cloudKeyringService || account != cloudKeyringAccount {
		return errors.New("unexpected storage key")
	}
	if s.err != nil {
		return s.err
	}
	s.value = value
	return nil
}
func (s *memoryCloudStore) Get(service, account string) (string, error) {
	if s.value == "" {
		return "", keyring.ErrNotFound
	}
	return s.value, nil
}
func (s *memoryCloudStore) Delete(service, account string) error { s.value = ""; return nil }
func TestCloudLoginCommandContract(t *testing.T) {
	for _, args := range [][]string{{"cloud", "auth", "login", "--help"}, {"cloud", "--help"}} {
		var out, errOut bytes.Buffer
		if code := Run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "spine cloud auth login") {
			t.Fatalf("help failed: %d %s", code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"cloud", "auth", "login", "--json"}, &out, &errOut); code == 0 {
		t.Fatal("interactive login must not accept JSON mode")
	}
	if got := fmt.Sprint(out.String(), errOut.String()); strings.Contains(got, "access_token") || strings.Contains(got, "refresh_token") {
		t.Fatal("token leaked")
	}
}
