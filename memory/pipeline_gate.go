package memory

import (
	"regexp"
	"strings"
)

// looksLikePromptInjection is a function variable seam for testing.
// It returns true if content appears to be a prompt injection attempt.
var looksLikePromptInjection = func(content string) bool {
	lower := strings.ToLower(content)
	for _, pattern := range injectionPatterns {
		if pattern.MatchString(lower) {
			return true
		}
	}
	return false
}

// injectionPatterns are regex patterns detecting common prompt injection attempts.
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+instructions`),
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?prior\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)\s+instructions`),
	regexp.MustCompile(`(?i)forget\s+(all\s+)?(previous|prior|your)\s+instructions`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an)\s+`),
	regexp.MustCompile(`(?i)new\s+instructions?:\s*`),
	regexp.MustCompile(`(?i)system\s*prompt\s*override`),
	regexp.MustCompile(`(?i)\bdo\s+not\s+follow\s+(any|your)\s+(previous|prior|original)`),
	regexp.MustCompile(`(?i)act\s+as\s+if\s+you\s+have\s+no\s+restrictions`),
	regexp.MustCompile(`(?i)pretend\s+(you\s+are|to\s+be)\s+`),
}

// ShouldCaptureL0 is a permissive gate — L0 captures everything for raw archival.
// It always returns true because L0 is an append-only log.
func ShouldCaptureL0(_ string) bool {
	return true
}

// ShouldExtractL1 is a strict gate — L1 rejects prompt injection before extraction.
// Returns false for content that looks like a prompt injection attempt.
// Returns false for empty or very short content (< 10 chars).
func ShouldExtractL1(content string) bool {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) < 10 {
		return false
	}
	if looksLikePromptInjection(trimmed) {
		return false
	}
	return true
}

// StripCodeBlocks removes fenced code blocks (```) from content.
// Used to clean assistant messages before L1 extraction.
func StripCodeBlocks(content string) string {
	// Match triple-backtick blocks including optional language identifier.
	re := regexp.MustCompile("(?s)```[a-zA-Z]*\n?.*?```")
	return re.ReplaceAllString(content, "")
}
