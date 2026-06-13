package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// maxJSONDepth is the maximum nesting depth allowed in JSON payloads.
	// Inputs exceeding this are rejected to prevent stack exhaustion.
	maxJSONDepth = 100

	// maxTokenLen is the maximum byte length of a zero-delimiter content block.
	// Inputs exceeding this are truncated to protect the ingestion pipeline.
	maxTokenLen = 20 * 1024 // 20 KB

	// maxContentLen is the maximum total byte length of a single message content
	// field before offloading (separate from the zero-delimiter guard).
	maxContentLen = 1 * 1024 * 1024 // 1 MB hard cap
)

// validRoles is the set of role values accepted in capture requests.
// Any other value is sanitized to "user" to block role escalation.
var validRoles = map[string]bool{
	"user":      true,
	"assistant": true,
	"system":    true,
}

// SanitizeRole validates a role field value.
// Unknown or privileged identifiers (e.g. "admin", "root") are replaced with
// "user" to prevent role escalation through the capture endpoint.
func SanitizeRole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	if validRoles[r] {
		return r
	}
	return "user"
}

// SanitizeContent validates and cleans a message content string.
// It rejects invalid UTF-8, enforces the hard-cap size limit, and truncates
// continuous zero-delimiter blocks exceeding maxTokenLen.
func SanitizeContent(content string) (string, error) {
	if !utf8.ValidString(content) {
		// Replace invalid sequences with the Unicode replacement character.
		content = strings.ToValidUTF8(content, "\uFFFD")
	}
	if len(content) > maxContentLen {
		content = content[:maxContentLen]
	}
	// Guard against long zero-delimiter attacks: if there are no spaces,
	// line breaks, or standard punctuation in a block exceeding maxTokenLen,
	// truncate at that boundary.
	content = truncateLongTokens(content)
	return content, nil
}

// truncateLongTokens scans content and truncates any run of non-whitespace,
// non-punctuation characters that exceeds maxTokenLen bytes.
func truncateLongTokens(s string) string {
	if len(s) <= maxTokenLen {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	runLen := 0
	for _, r := range s {
		if isDelimiter(r) {
			runLen = 0
		} else {
			runLen += utf8.RuneLen(r)
			if runLen > maxTokenLen {
				continue // drop the character
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isDelimiter reports whether r is a whitespace or common punctuation character.
func isDelimiter(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '.', ',', ';', ':', '!', '?', '/', '\\', '-', '_':
		return true
	}
	return false
}

// CheckJSONDepth verifies that the JSON value in data does not exceed maxDepth
// nesting levels. Returns an error if the depth limit is exceeded.
func CheckJSONDepth(data []byte, maxDepth int) error {
	depth, err := jsonDepth(data, maxDepth)
	if err != nil {
		return err
	}
	if depth > maxDepth {
		return fmt.Errorf("sanitize: JSON depth %d exceeds limit %d", depth, maxDepth)
	}
	return nil
}

// jsonDepth walks a JSON token stream and returns the maximum nesting depth.
func jsonDepth(data []byte, limit int) (int, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	depth := 0
	maxSeen := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
			if depth > maxSeen {
				maxSeen = depth
			}
			if depth > limit {
				return maxSeen, fmt.Errorf("sanitize: JSON depth exceeds %d", limit)
			}
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
	return maxSeen, nil
}
