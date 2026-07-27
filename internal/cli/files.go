package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

type writeOptions struct {
	DryRun bool
	Force  bool
}

func writeFile(path, contents string, options writeOptions) error {
	if _, err := os.Stat(path); err == nil && !options.Force {
		return fmt.Errorf("refusing to overwrite existing file: %s (use --force to replace it)", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if options.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}
