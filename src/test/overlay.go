// Generate Go's build overlay for tests stored under src/test.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, "test overlay:", err)
		os.Exit(1)
	}
}

func generate() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: go run overlay.go <source directory> <test directory>")
	}
	source, err := filepath.Abs(os.Args[1])
	if err != nil {
		return err
	}
	tests, err := filepath.Abs(os.Args[2])
	if err != nil {
		return err
	}
	if tests != filepath.Join(source, "test") {
		return fmt.Errorf("test directory must be %s", filepath.Join(source, "test"))
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == tests {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			return fmt.Errorf("test outside centralized directory: %s", path)
		}
		return nil
	}); err != nil {
		return err
	}

	replace := make(map[string]string)
	err = filepath.WalkDir(tests, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("test must be a regular file: %s", path)
		}
		relative, err := filepath.Rel(tests, path)
		if err != nil {
			return err
		}
		virtual := filepath.Join(source, relative)
		packageDir := filepath.Dir(virtual)
		entries, err := os.ReadDir(packageDir)
		if err != nil {
			return fmt.Errorf("test package %s: %w", packageDir, err)
		}
		hasSource := false
		for _, item := range entries {
			if item.Type().IsRegular() && strings.HasSuffix(item.Name(), ".go") && !strings.HasSuffix(item.Name(), "_test.go") {
				hasSource = true
				break
			}
		}
		if !hasSource {
			return fmt.Errorf("no production Go package at %s", packageDir)
		}
		replace[virtual] = path
		return nil
	})
	if err != nil {
		return err
	}
	if len(replace) == 0 {
		return fmt.Errorf("no Go tests found in %s", tests)
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Replace map[string]string
	}{Replace: replace})
}
