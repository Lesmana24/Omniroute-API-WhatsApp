package textutil

import (
	"strings"
	"testing"
)

func TestFormatWhatsAppText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bold markdown",
			input:    "ini **tebal** bang",
			expected: "ini *tebal* bang",
		},
		{
			name:     "Headers",
			input:    "### Judul\nini konten",
			expected: "*Judul*\nini konten",
		},
		{
			name:     "List items",
			input:    "- item 1\n- item 2",
			expected: "• item 1\n• item 2",
		},
		{
			name:     "Horizontal rules",
			input:    "konten\n---\nlebih banyak konten",
			expected: "konten\n\nlebih banyak konten",
		},
		{
			name:     "Italic __",
			input:    "ini __miring__",
			expected: "ini _miring_",
		},
		{
			name:     "Italic __ in middle",
			input:    "hello __world__ test",
			expected: "hello _world_ test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatWhatsAppText(tt.input)
			if got != tt.expected {
				t.Errorf("FormatWhatsAppText() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestChunkTextShort(t *testing.T) {
	text := "Halo, ini pesan singkat."
	chunks := ChunkText(text, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected '%s', got '%s'", text, chunks[0])
	}
}

func TestChunkTextLongParagraph(t *testing.T) {
	part1 := "Bagian pertama adalah paragraf penting yang harus disampaikan kepada pengguna WhatsApp."
	part2 := "Bagian kedua memberikan rincian teknis mendalam tentang AI Omniroute integrasi."
	text := part1 + "\n\n" + part2

	chunks := ChunkText(text, len([]rune(part1))+10)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0], part1) {
		t.Errorf("chunk 0 should contain part1")
	}
	if !strings.Contains(chunks[1], part2) {
		t.Errorf("chunk 1 should contain part2")
	}
}

func TestChunkTextUnicodeRunes(t *testing.T) {
	// Emoji and complex unicode
	emojiStr := strings.Repeat("🚀🌟🔥🤖", 100)
	chunks := ChunkText(emojiStr, 50)
	if len(chunks) <= 1 {
		t.Errorf("expected multiple chunks for unicode string, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len([]rune(c)) > 50 {
			t.Errorf("chunk %d exceeded limit: %d runes", i, len([]rune(c)))
		}
	}
}
