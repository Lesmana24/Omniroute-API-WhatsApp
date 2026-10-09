package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
)

func TestOmnirouteService_GenerateResponse(t *testing.T) {
	// Create mock HTTP server simulating Omniroute OpenAI-compatible API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected Bearer test-api-key, got %s", r.Header.Get("Authorization"))
		}

		var req domain.OmnirouteChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}

		// Verify messages include system instruction, history, and current prompt
		if len(req.Messages) != 3 {
			t.Errorf("expected 3 messages, got %d", len(req.Messages))
		}

		resp := domain.OmnirouteChatResponse{
			ID:    "chatcmpl-123",
			Model: "omniroute-default",
			Choices: []domain.OmnirouteChatChoice{
				{
					Index: 0,
					Message: domain.OmnirouteMessage{
						Role:    "assistant",
						Content: "Halo! Saya asisten Omniroute AI siap membantu Anda.",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &config.Config{
		OmnirouteAPIBaseURL: server.URL,
		OmnirouteAPIKey:     "test-api-key",
		OmnirouteModel:      "omniroute-default",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewOmnirouteService(cfg, logger)

	history := []domain.ChatMessage{
		{
			PhoneNumber: "628123456789",
			Role:        domain.RoleUser,
			Content:     "Halo",
		},
	}

	reply, err := svc.GenerateResponse(context.Background(), history, "Siapa nama kamu?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Halo! Saya asisten Omniroute AI siap membantu Anda."
	if reply != expected {
		t.Errorf("expected '%s', got '%s'", expected, reply)
	}
}

func TestOmnirouteService_FallbackResponse(t *testing.T) {
	// Mock server returning flat fallback JSON response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := domain.OmnirouteChatResponse{
			Response: "Balasan AI via fallback field response.",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &config.Config{
		OmnirouteAPIBaseURL: server.URL,
		OmnirouteAPIKey:     "test-key",
		OmnirouteModel:      "omniroute-default",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewOmnirouteService(cfg, logger)
	reply, err := svc.GenerateResponse(context.Background(), nil, "Halo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if reply != "Balasan AI via fallback field response." {
		t.Errorf("expected fallback reply, got '%s'", reply)
	}
}
