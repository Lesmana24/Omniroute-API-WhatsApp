package textutil

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// DefaultMaxChunkSize is the safe limit for WhatsApp messages (max is ~4096).
const DefaultMaxChunkSize = 3800

// FormatWhatsAppText converts Markdown-like text to WhatsApp-compatible format.
func FormatWhatsAppText(text string) string {
	// 1. Remove Markdown headers: ### Title -> *Title*
	// Handle optional leading spaces and multiple hashes
	reHeader := regexp.MustCompile(`(?m)^\s*#{1,6}\s+(.*)$`)
	text = reHeader.ReplaceAllString(text, "*$1*")

	// 2. Bold: **text** -> *text*
	reBold := regexp.MustCompile(`\*\*(.*?)\*\*`)
	text = reBold.ReplaceAllString(text, "*$1*")

	// 3. Italic: __text__ -> _text_
	reItalic := regexp.MustCompile(`__(.*?)__`)
	text = reItalic.ReplaceAllString(text, "_${1}_")

	// 4. Strikethrough: ~~text~~ -> ~text~
	reStrike := regexp.MustCompile(`~~(.*?)~~`)
	text = reStrike.ReplaceAllString(text, "~$1~")

	// 5. Remove horizontal rules: --- or *** or ___
	reHR := regexp.MustCompile(`(?m)^\s*[\s-*_]{3,}\s*$`)
	text = reHR.ReplaceAllString(text, "")

	// 6. Convert unordered lists: - item or * item or · item -> • item
	// Also handle standard AI middle dot bullet
	reList := regexp.MustCompile(`(?m)^\s*([-*+·•])\s+`)
	text = reList.ReplaceAllString(text, "• ")

	// 7. Clean up multiple empty lines (max 2 consecutive)
	reEmptyLines := regexp.MustCompile(`\n{3,}`)
	text = reEmptyLines.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// ChunkText splits a long text string into smaller parts suitable for WhatsApp messages.
// It prioritizes splitting at paragraph breaks, newlines, sentence ends, or word spaces.
func ChunkText(text string, maxChunkSize int) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	if maxChunkSize <= 0 {
		maxChunkSize = DefaultMaxChunkSize
	}

	// If total runes is already within limit, return directly
	if utf8.RuneCountInString(trimmed) <= maxChunkSize {
		return []string{trimmed}
	}

	var chunks []string
	remaining := trimmed

	for len(remaining) > 0 {
		runes := []rune(remaining)
		if len(runes) <= maxChunkSize {
			chunks = append(chunks, remaining)
			break
		}

		// Look for best split point within the first maxChunkSize runes
		chunkCandidate := string(runes[:maxChunkSize])
		splitIndex := findOptimalSplitIndex(chunkCandidate)

		if splitIndex <= 0 {
			// No suitable delimiter found; hard split at maxChunkSize runes
			chunks = append(chunks, string(runes[:maxChunkSize]))
			remaining = strings.TrimLeft(string(runes[maxChunkSize:]), " \t\r\n")
		} else {
			chunks = append(chunks, strings.TrimRight(string([]rune(chunkCandidate)[:splitIndex]), " \t\r\n"))
			remaining = strings.TrimLeft(string(runes[splitIndex:]), " \t\r\n")
		}
	}

	// Filter out any empty chunks resulting from trimming
	result := make([]string, 0, len(chunks))
	for _, c := range chunks {
		c = strings.TrimSpace(c)
		if c != "" {
			result = append(result, c)
		}
	}

	return result
}

// findOptimalSplitIndex searches backwards for natural punctuation and word boundaries.
func findOptimalSplitIndex(s string) int {
	runes := []rune(s)
	total := len(runes)

	// Don't cut too early; require at least 50% length before searching for splits
	minCutoff := total / 2
	if minCutoff < 1 {
		minCutoff = 1
	}

	// 1. Try double newline (paragraph boundary)
	for i := total - 1; i >= minCutoff; i-- {
		if i+1 < total && runes[i] == '\n' && runes[i+1] == '\n' {
			return i + 2
		}
	}

	// 2. Try single newline
	for i := total - 1; i >= minCutoff; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}

	// 3. Try sentence endings (". ", "! ", "? ")
	for i := total - 1; i >= minCutoff; i-- {
		if (runes[i] == '.' || runes[i] == '!' || runes[i] == '?') && i+1 < total && (runes[i+1] == ' ' || runes[i+1] == '\n') {
			return i + 2
		}
	}

	// 4. Try whitespace boundary
	for i := total - 1; i >= minCutoff; i-- {
		if runes[i] == ' ' {
			return i + 1
		}
	}

	return -1
}
