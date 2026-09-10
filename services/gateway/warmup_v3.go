package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func envEnabledV3(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(optionalEnv(key, "")))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return fallback
	}
}

func (s *server) warmAgentStartupV3() {
	if !envEnabledV3("AGENT_STARTUP_WARMUP", true) {
		fmt.Println("agent startup warm-up disabled")
		return
	}
	started := time.Now()
	timeout := time.Duration(optionalIntEnv("AGENT_STARTUP_WARMUP_TIMEOUT_SECONDS", 60)) * time.Second
	deadline := time.Now().Add(timeout)
	endpoint := strings.TrimSuffix(s.cfg.AgentURL, "/v1/chat/completions") + "/v1/models"
	ready := false
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		status := s.check(ctx, endpoint)
		cancel()
		if status == "ready" {
			ready = true
			break
		}
		wait := time.Duration(attempt) * 200 * time.Millisecond
		if wait > time.Second {
			wait = time.Second
		}
		time.Sleep(wait)
	}
	if !ready {
		fmt.Printf("agent startup warm-up skipped after %.1fs; model endpoint not ready\n", time.Since(started).Seconds())
		return
	}

	messages := []message{{Role: "system", Content: generalAssistantPromptV2}, {Role: "user", Content: "Reply OK."}}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		callTimeout := remaining
		if callTimeout > 30*time.Second {
			callTimeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		_, lastErr = s.callAgentV2(ctx, messages, 2, 0, nil, false, nil)
		cancel()
		if lastErr == nil {
			fmt.Printf("agent startup warm-up complete in %.1fs\n", time.Since(started).Seconds())
			return
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	if lastErr != nil {
		fmt.Printf("agent startup warm-up incomplete after %.1fs: %s\n", time.Since(started).Seconds(), sanitizeRouterErrorV2(lastErr))
	}
}
