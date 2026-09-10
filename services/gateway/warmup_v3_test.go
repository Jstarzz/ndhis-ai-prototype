package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWarmAgentStartupV3UsesAssistantPrefix(t *testing.T) {
	t.Setenv("AGENT_STARTUP_WARMUP", "1")
	t.Setenv("AGENT_STARTUP_WARMUP_TIMEOUT_SECONDS", "3")
	var completions atomic.Int32
	var captured agentRequestV2
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		case "/v1/chat/completions":
			completions.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode warm-up request: %v", err)
			}
			writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "OK"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	s := &server{
		cfg: config{AgentURL: upstream.URL + "/v1/chat/completions", AgentModel: "test-agent", AgentMaxTokens: 128},
		client: &http.Client{Timeout: 2 * time.Second},
	}
	s.warmAgentStartupV3()

	if completions.Load() != 1 {
		t.Fatalf("warm-up completion calls = %d, want 1", completions.Load())
	}
	if captured.MaxTokens != 2 {
		t.Fatalf("max tokens = %d, want 2", captured.MaxTokens)
	}
	if !captured.CachePrompt {
		t.Fatal("warm-up must enable prompt caching")
	}
	if captured.Stream {
		t.Fatal("warm-up must not stream")
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" || captured.Messages[0].Content != generalAssistantPromptV2 {
		t.Fatalf("warm-up did not use the exact assistant prefix: %#v", captured.Messages)
	}
	if captured.Messages[1].Content != "Reply OK." {
		t.Fatalf("warm-up user message = %q", captured.Messages[1].Content)
	}
	if captured.ReasoningEffort != "none" {
		t.Fatalf("reasoning effort = %q", captured.ReasoningEffort)
	}
	if enabled, ok := captured.ChatTemplateKwargs["enable_thinking"].(bool); !ok || enabled {
		t.Fatalf("enable_thinking = %#v, want false", captured.ChatTemplateKwargs["enable_thinking"])
	}
}

func TestWarmAgentStartupV3CanBeDisabled(t *testing.T) {
	t.Setenv("AGENT_STARTUP_WARMUP", "0")
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
	}))
	defer upstream.Close()

	s := &server{
		cfg: config{AgentURL: upstream.URL + "/v1/chat/completions", AgentModel: "test-agent", AgentMaxTokens: 128},
		client: &http.Client{Timeout: time.Second},
	}
	s.warmAgentStartupV3()
	if requests.Load() != 0 {
		t.Fatalf("disabled warm-up made %d requests", requests.Load())
	}
}
