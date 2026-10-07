package textutil

import (
	"strings"
	"testing"
)

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
