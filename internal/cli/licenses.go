package cli

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
)

//go:embed licenses/*
var embeddedLicenses embed.FS

func runLicenses(args []string, out, errOut io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errOut, "usage: spine licenses")
		return 1
	}
	entries, err := fs.ReadDir(embeddedLicenses, "licenses")
	if err != nil {
		fmt.Fprintf(errOut, "could not read embedded license notices: %v\n", err)
		return 1
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contents, err := embeddedLicenses.ReadFile(path.Join("licenses", entry.Name()))
		if err != nil {
			fmt.Fprintf(errOut, "could not read embedded license %q: %v\n", entry.Name(), err)
			return 1
		}
		if _, err := fmt.Fprintf(out, "--- %s ---\n%s", entry.Name(), contents); err != nil {
			fmt.Fprintf(errOut, "could not write license output: %v\n", err)
			return 1
		}
		if len(contents) > 0 && contents[len(contents)-1] != '\n' {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out)
	}
	return 0
}
