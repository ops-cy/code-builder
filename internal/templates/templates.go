// Package templates serves the built-in Dockerfile templates.
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed *.Dockerfile
var files embed.FS

// Has reports whether a template with the given file name exists.
func Has(name string) bool {
	_, err := files.ReadFile(name)
	return err == nil
}

// Get returns the template content by file name.
func Get(name string) ([]byte, error) {
	b, err := files.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("templates: unknown template %q (available: %v)", name, Names())
	}
	return b, nil
}

// Names lists all embedded template file names, sorted.
func Names() []string {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}
