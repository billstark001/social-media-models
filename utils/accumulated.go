package utils

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

var accumulatedName = regexp.MustCompile(`^acc-state-(\d+|\d{4}-\d{2}-\d{2}T\d{6}Z)\.lz4$`)

// LatestAccumulatedState selects a legacy history by completed step or UTC
// generation without decoding it. It accepts basenames or paths and rejects
// ambiguous generations, sharing the same ordering for reading and recovery.
func LatestAccumulatedState(names []string) (string, error) {
	latest, scheme := "", ""
	var maximum int64
	seen := make(map[int64]bool, len(names))
	for _, name := range names {
		match := accumulatedName.FindStringSubmatch(filepath.Base(name))
		if match == nil {
			return "", fmt.Errorf("unsupported accumulated-state filename: %s", name)
		}
		stamp, current := match[1], "step"
		var key int64
		var err error
		if stamp[len(stamp)-1] == 'Z' {
			current = "utc"
			var generation time.Time
			generation, err = time.Parse("2006-01-02T150405Z", stamp)
			if err == nil && generation.Year() < 1 {
				err = fmt.Errorf("year must be positive")
			}
			key = generation.Unix()
		} else {
			key, err = strconv.ParseInt(stamp, 10, 64)
		}
		if err != nil {
			return "", fmt.Errorf("invalid accumulated-state generation in %s: %w", name, err)
		}
		if scheme != "" && current != scheme {
			return "", fmt.Errorf("mixed step and UTC accumulated-state filenames have no common ordering")
		}
		scheme = current
		if seen[key] {
			return "", fmt.Errorf("multiple accumulated-state files have the same generation")
		}
		seen[key] = true
		if latest == "" || key > maximum {
			latest, maximum = name, key
		}
	}
	return latest, nil
}
