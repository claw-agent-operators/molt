// SPDX-License-Identifier: AGPL-3.0-or-later
package sync

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/robfig/cron/v3"
)

// NextTick returns the next scheduled run time given a schedule string.
// The schedule can be a cron expression (e.g. "0 * * * *") or a duration
// interval (e.g. "1h", "15m", "300000" milliseconds).
func NextTick(schedule string) (time.Time, error) {
	if isCronExpression(schedule) {
		p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		s, err := p.Parse(schedule)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", schedule, err)
		}
		return s.Next(time.Now()), nil
	}

	d, err := parseDuration(schedule)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(d), nil
}

// IsDeltaRun returns true if the next run should be a delta (incremental) export.
// It returns false (full export) when no full has ever been done or the full_every
// interval has elapsed.
func IsDeltaRun(state *SyncState, cfg *SyncConfig) bool {
	if state.LastFullAt == "" {
		return false // no full yet
	}
	fullEvery, err := parseDuration(cfg.FullEvery)
	if err != nil {
		return false // bad config → be safe, do full
	}
	t, err := time.Parse(time.RFC3339, state.LastFullAt)
	if err != nil {
		return false
	}
	return time.Since(t) < fullEvery
}

// parseDuration parses molt duration strings: "7d", "1h", "15m", or plain
// millisecond integers like "300000".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	// Plain integer → milliseconds
	if isDigits(s) {
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(ms) * time.Millisecond, nil
	}

	// Days suffix
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}

	// Standard Go duration (h, m, s)
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

// isCronExpression returns true when s looks like a cron expression (has spaces
// between fields and at least one non-digit field character).
func isCronExpression(s string) bool {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		for _, r := range f {
			if !unicode.IsDigit(r) && r != '*' && r != '/' && r != '-' && r != ',' {
				return false
			}
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
