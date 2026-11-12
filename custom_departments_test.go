package main

// Custom departments are not being built. The API ships no custom-department
// feature, but its triage notes cited a planned "Custom Departments" work item
// as a dependency for two requests, which would send a reader back to a plan
// that no longer exists. This walks the repository's own text rather than
// naming those two files, so a new note that leans on the cancelled plan fails
// here.
//
// What is being guarded is the *plan*, not the words. Triaging a request as
// probably_not has to say which cancelled feature it is and that it was
// cancelled, or the decline is unreadable — so a line that records the
// cancellation passes, and only a line that still treats the plan as live
// fails. A line that does both ("cancelled, but planned for later") fails too:
// that is the thing this exists to catch.
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

// Records that the plan is dead: the reader is being told not to expect it.
var cancelledMarkers = regexp.MustCompile(`(?i)\bcancelled\b|\bcanceled\b|\bnot being built\b|\brather than built\b|\bwas removed\b|\bwere removed\b|\bremoved from every surface\b`)

// Treats the plan as live: the reader is being sent back to it.
var plannedMarkers = regexp.MustCompile(`(?i)\bplanned\b|\b(?:are|is|be|being)\s+coming\b|\bupcoming\b|\bon the roadmap\b|\bwill be built\b|\bdepends on\b|\bblocked by\b|\bwaiting on\b|\bonce (?:the )?custom\b`)

// mentionsLiveCustomDepartmentPlan reports whether a line points a reader at
// the cancelled custom-departments plan as though it were still coming.
func mentionsLiveCustomDepartmentPlan(line string) bool {
	if !customDepartmentMentions.MatchString(line) {
		return false
	}
	if plannedMarkers.MatchString(line) {
		return true
	}
	return !cancelledMarkers.MatchString(line)
}

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
				if mentionsLiveCustomDepartmentPlan(line) {
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
		t.Errorf("custom departments are not being built, but these lines still point a reader at the plan:\n  %s\n"+
			"(a line may name the feature if it also records that it was cancelled)",
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

// The walk above only reports line numbers, so the judgement it depends on is
// covered directly here.
func TestMentionsLiveCustomDepartmentPlan(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{
			"a note leaning on the plan as a dependency",
			"risks_or_deps: depends on the Custom Departments work item",
			true,
		},
		{
			"a note promising it later",
			"Custom departments are planned for a future release.",
			true,
		},
		{
			"a bare mention with no verdict",
			"See the custom departments feature.",
			true,
		},
		{
			"a triage note recording the cancellation",
			"A DMV department means a custom department, and custom departments are cancelled: the template was removed from every surface in Nov 2026 rather than built.",
			false,
		},
		{
			"a note saying it is not being built",
			"Custom teams is the custom-departments feature, which is not being built.",
			false,
		},
		{
			"cancelled but still promised is still a failure",
			"The old plan was cancelled; custom departments are coming soon instead.",
			true,
		},
		{
			// "Coming Soon" is the name of what was taken down, not a promise.
			"describing the removal of the Coming Soon notices",
			"Both are the custom-departments feature, which is cancelled: the Custom template and its Coming Soon notices were removed from every surface in Nov 2026 rather than built.",
			false,
		},
		{
			"rank custom requirements are unrelated",
			"metricType \"custom\" requirements are evaluated per rank.",
			false,
		},
		{
			"dispatch custom tones are unrelated",
			"Communities can upload custom tone sounds.",
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mentionsLiveCustomDepartmentPlan(c.line); got != c.want {
				t.Errorf("mentionsLiveCustomDepartmentPlan(%q) = %v, want %v", c.line, got, c.want)
			}
		})
	}
}
