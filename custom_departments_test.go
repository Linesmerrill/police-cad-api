package main

// Custom departments are not being built. The API ships no custom-department
// feature, but its triage notes cited a planned "Custom Departments" work item
// as a dependency for two requests, which would send a reader back to a plan
// that no longer exists. This walks the repository's own text rather than
// naming those two files, so a new note that leans on the cancelled plan fails
// here.
//
// Rank "custom" requirements, dispatch custom tones and custom currencies are
// unrelated and are not matched.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var customDepartmentMentions = regexp.MustCompile(`(?i)custom departments?|custom department plan|custom templates?`)

// Directories whose contents are ours to keep accurate. vendor/ and the module
// cache are other people's text.
var searchedDirs = []string{"api", "databases", "models", "docs", "scripts", "templates", "config"}

var searchedExtensions = map[string]bool{
	".go": true, ".md": true, ".json": true, ".js": true, ".py": true, ".yml": true, ".yaml": true,
}

func TestNoCustomDepartmentMentions(t *testing.T) {
	var hits []string

	for _, dir := range searchedDirs {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "vendor" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				// scripts/feature-triage/data holds a fetched copy of the
				// public feature-request threads: what members and staff
				// actually wrote, including a staff comment promising Custom
				// Departments. Editing a record of someone's words to match a
				// decision made later would be a falsification, so it is read
				// as evidence and left alone. The promise on the live site is a
				// separate thing to retract, on the site.
				if filepath.ToSlash(path) == "scripts/feature-triage/data" {
					return filepath.SkipDir
				}
				return nil
			}
			if !searchedExtensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(body), "\n") {
				if customDepartmentMentions.MatchString(line) {
					hits = append(hits, filepath.ToSlash(path)+":"+itoa(i+1))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	if len(hits) > 0 {
		t.Errorf("custom departments are not being built, but they are still mentioned at:\n  %s",
			strings.Join(hits, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
