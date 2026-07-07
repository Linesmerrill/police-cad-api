package models

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// SentenceModeConsecutive sums the jail time of every charge (the default).
	SentenceModeConsecutive = "consecutive"
	// SentenceModeConcurrent uses only the single longest charge.
	SentenceModeConcurrent = "concurrent"
	// LifeSentinelSeconds marks an indeterminate ("Life") sentence in the numeric
	// TotalJailTimeSeconds field; display layers should render LifeLabel instead.
	LifeSentinelSeconds = int64(-1)
	// LifeLabel is the human-readable label for an indeterminate sentence.
	LifeLabel = "Life"
)

// jailSegmentRe grabs "<number> <unit-word>" pairs so multi-part strings like
// "1 year 2 months" total correctly.
var jailSegmentRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*([a-zA-Z]+)`)

// lifeRe matches indeterminate sentences that cannot be summed numerically.
var lifeRe = regexp.MustCompile(`(?i)\b(life|perp|perpetual|death|permanent)\b`)

// jailUnitSeconds maps recognised unit words (and common abbreviations) to
// seconds. A month is treated as 30 days and a year as 365 days.
var jailUnitSeconds = map[string]int64{
	"second": 1, "seconds": 1, "sec": 1, "secs": 1,
	"minute": 60, "minutes": 60, "min": 60, "mins": 60,
	"hour": 3600, "hours": 3600, "hr": 3600, "hrs": 3600,
	"day": 86400, "days": 86400,
	"week": 604800, "weeks": 604800, "wk": 604800, "wks": 604800,
	"month": 2592000, "months": 2592000, "mo": 2592000, "mos": 2592000,
	"year": 31536000, "years": 31536000, "yr": 31536000, "yrs": 31536000,
}

// ParseJailTimeSeconds converts a free-form jail-time string ("30 seconds",
// "6 months", "1 year 2 months", "Life", "N/A", "") into a total number of
// seconds. It returns isLife=true for indeterminate sentences (seconds is then
// 0); callers should surface those as "Life" rather than a number.
func ParseJailTimeSeconds(raw string) (seconds int64, isLife bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || s == "n/a" || s == "na" || s == "none" || s == "0" {
		return 0, false
	}
	if lifeRe.MatchString(s) {
		return 0, true
	}
	var total int64
	for _, m := range jailSegmentRe.FindAllStringSubmatch(s, -1) {
		unit, ok := jailUnitSeconds[strings.ToLower(m[2])]
		if !ok {
			continue // unrecognised unit word — skip rather than guess
		}
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		total += int64(math.Round(val * float64(unit)))
	}
	return total, false
}

// NormalizeSentenceMode returns a valid sentence mode, defaulting to
// consecutive for anything other than an explicit "concurrent".
func NormalizeSentenceMode(mode string) string {
	if strings.ToLower(strings.TrimSpace(mode)) == SentenceModeConcurrent {
		return SentenceModeConcurrent
	}
	return SentenceModeConsecutive
}

// ComputeArrestTotals recomputes the fine + jail-time totals for a set of
// charges. Fines always sum; jail time sums for "consecutive" (default) or
// takes the single longest charge for "concurrent". Any "Life" charge makes the
// whole sentence Life (LifeSentinelSeconds / LifeLabel).
func ComputeArrestTotals(charges []ArrestCharge, mode string) (totalFine float64, totalSeconds int64, label string) {
	var sum, longest int64
	var anyLife bool
	for _, c := range charges {
		totalFine += c.Amount
		secs, life := ParseJailTimeSeconds(c.JailTime)
		if life {
			anyLife = true
		}
		sum += secs
		if secs > longest {
			longest = secs
		}
	}
	if anyLife {
		return totalFine, LifeSentinelSeconds, LifeLabel
	}
	if NormalizeSentenceMode(mode) == SentenceModeConcurrent {
		totalSeconds = longest
	} else {
		totalSeconds = sum
	}
	return totalFine, totalSeconds, FormatDuration(totalSeconds)
}

// FormatDuration renders seconds as up to the two most-significant non-zero
// units, e.g. 2592045 -> "1 month 45 seconds". Zero (or less) renders "None".
func FormatDuration(seconds int64) string {
	if seconds <= 0 {
		return "None"
	}
	units := []struct {
		label string
		secs  int64
	}{
		{"year", 31536000}, {"month", 2592000}, {"day", 86400},
		{"hour", 3600}, {"minute", 60}, {"second", 1},
	}
	var parts []string
	remaining := seconds
	for _, u := range units {
		if remaining < u.secs {
			continue
		}
		n := remaining / u.secs
		remaining %= u.secs
		plural := ""
		if n != 1 {
			plural = "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s%s", n, u.label, plural))
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " ")
}
