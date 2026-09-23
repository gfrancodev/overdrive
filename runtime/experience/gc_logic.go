package main

import (
	"time"
)

func gcProtectedSource(source string) bool {
	return source == "user_feedback" || source == "project_instruction" || source == "adr"
}

func gcDeprecatedCutoff(now time.Time) string {
	return now.AddDate(0, 0, -90).Format(time.RFC3339)
}

func gcStaleCutoff(now time.Time) string {
	return now.AddDate(0, 0, -180).Format(time.RFC3339)
}

func shouldDeleteDeprecatedMemory(updatedAt, cutoff string) bool {
	return updatedAt < cutoff
}

func shouldDeleteStaleMemory(source, updatedAt, cutoff string, confidence float64) bool {
	if gcProtectedSource(source) {
		return false
	}
	return confidence < 0.25 && updatedAt < cutoff
}
