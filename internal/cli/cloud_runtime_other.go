//go:build !linux

package cli

import "errors"

func startCloudRuntime(string) error { return errors.New("cloud runtime requires Linux") }
