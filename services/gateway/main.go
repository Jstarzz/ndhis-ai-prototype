package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type config struct {
	RuntimeProfile   string
	DemoKey          string
	AgentURL         string
	AgentModel       string
	TranslationURL   string
	ForecastURL      string
	RadiologyURL     string
	AuditPath        string
	RequestsPerMin   int
	MaxConcurrent    int
	MaxBodyBytes     int64
	ASRModel         string
	TranslationModel string
	ForecastModel    string
	RadiologyModel   string
}

type server struct {
	cfg       config
	client    *http.Client
	auditMu   sync.Mutex
	rateMu    sync.Mutex
	rates     map[string]rateState
	semaphore chan struct{}
}

type rateState struct {
	Window time.Time
	Count  int
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages []message `json:"messages"`
}

type agentRequest struct {
	Model          string            `json:"model"`
	Messages       []message         `json:"messages"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens"`
	ResponseFormat map[string]string `json:"response_format"`
}

type agentResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type agentDecision struct {
	Type      string         `json:"type"`
	Content   string         `json:"content,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type chatResponse struct {
	Answer    string         `json:"answer"`
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	LatencyMS int64          `json:"latency_ms"`
}

type auditEvent struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"request_id"`
	User      string `json:"user"`
	Role      string `json:"role"`
	Route     string `json:"route"`
	Tool      string `json:"tool,omitempty"`
	Model     string `json:"model,omitempty"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
}

var toolManifest = []map[string]any{
	{"name": "forecast_patient_volume", "description": "Forecast patient arrivals for a facility and department", "arguments": map[string]any{"facility": "string", "department": "string", "horizon_days": "integer"}},
	{"name": "forecast_bed_occupancy", "description": "Forecast bed occupancy for a facility and department", "arguments": map[string]any{"facility": "string", "department": "string", "horizon_days": "integer"}},
	{"name": "forecast_disease_incidence", "description": "Forecast disease incidence for a named disease category", "arguments": map[string]any{"facility": "string", "disease": "string", "horizon_days": "integer"}},
	{"name": "get_radiology_result", "description": "Retrieve an already analyzed radiology study by result id", "arguments": map[string]any{"result_id": "string"}},
	{"name": "get_service_status", "description": "Get local AI service status", "arguments": map[string]any{}},
}

func main() {
	cfg := config{
		RuntimeProfile:   mustEnv("RUNTIME_PROFILE"),
		DemoKey:          mustEnv("DEMO_API_KEY"),
		AgentURL:         mustEnv("AGENT_URL"),
		AgentModel:       mustEnv("AGENT_MODEL_NAME"),
		TranslationURL:   mustEnv("TRANSLATION_URL"),
		ForecastURL:      mustEnv("FORECAST_URL"),
		RadiologyURL:     mustEnv("RADIOLOGY_URL"),
		AuditPath:        mustEnv("AUDIT_PATH"),
		RequestsPerMin:   mustIntEnv("REQUESTS_PER_MINUTE"),
		MaxConcurrent:    mustIntEnv("MAX_CONCURRENT_REQUESTS"),
		MaxBodyBytes:     int64(mustIntEnv("MAX_BODY_MB")) * 1024 * 1024,
		ASRModel:         mustEnv("ASR_MODEL_NAME"),
		TranslationModel: mustEnv("TRANSLATION_MODEL_NAME"),
		ForecastModel:    mustEnv("FORECAST_MODEL_NAME"),
		RadiologyModel:   mustEnv("RADIOLOGY_MODEL_NAME"),
	}

	s := &server{
		cfg:       cfg,
		client:    &http.Client{Timeout: 90 * time.Second},
		rates:     map[string]rateState{},
		semaphore: make(chan struct{}, cfg.MaxConcurrent),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/system", s.guard(s.systemInfo))
	mux.HandleFunc("POST /api/chat", s.guard(s.chat))
	mux.HandleFunc("POST /api/forecast", s.guard(s.forecastProxy))
	mux.HandleFunc("POST /api/radiology", s.guard(s.radiologyProxy))
	mux.HandleFunc("GET /api/radiology/{id}", s.guard(s.radiologyResultProxy))

	log.Println("gateway listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", s.cors(mux)))
}

func mustEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		panic("missing environment variable: " + key)
	}
	return value
}

func mustIntEnv(key string) int {
	value := mustEnv(key)
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		panic("invalid positive integer environment variable: " + key)
	}
	return parsed
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-NDHIS-Demo-Key, X-NDHIS-User, X-NDHIS-Role")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-NDHIS-Demo-Key") != s.cfg.DemoKey {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		user := strings.TrimSpace(r.Header.Get("X-NDHIS-User"))
		role := strings.TrimSpace(r.Header.Get("X-NDHIS-Role"))
		if user == "" || role == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "identity headers required"})
			return
		}
		if !strings.EqualFold(role, "doctor") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "doctor role required for this prototype"})
			return
		}
		if !s.allow(user) {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}
		select {
		case s.semaphore <- struct{}{}:
			defer func() { <-s.semaphore }()
		default:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "AI gateway at concurrency limit"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		next(w, r)
	}
}

func (s *server) allow(user string) bool {
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	state := s.rates[user]
	if state.Window.IsZero() || now.Sub(state.Window) >= time.Minute {
		s.rates[user] = rateState{Window: now, Count: 1}
		return true
	}
	if state.Count >= s.cfg.RequestsPerMin {
		return false
	}
	state.Count++
	s.rates[user] = state
	return true
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "services": s.serviceStatus(ctx)})
}

func (s *server) systemInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"processing": "local",
		"profile":    s.cfg.RuntimeProfile,
		"services":   s.serviceStatus(ctx),
		"models": map[string]string{
			"agent":       s.cfg.AgentModel,
			"asr":         s.cfg.ASRModel,
			"translation": s.cfg.TranslationModel,
			"forecasting": s.cfg.ForecastModel,
			"radiology":   s.cfg.RadiologyModel,
		},
		"limits": map[string]any{
			"requests_per_minute_per_user": s.cfg.RequestsPerMin,
			"max_concurrent_requests":      s.cfg.MaxConcurrent,
			"max_body_bytes":               s.cfg.MaxBodyBytes,
		},
	})
}

func (s *server) serviceStatus(ctx context.Context) map[string]string {
	checks := map[string]string{}
	checks["agent"] = s.check(ctx, strings.TrimSuffix(s.cfg.AgentURL, "/v1/chat/completions")+"/v1/models")
	checks["forecasting"] = s.check(ctx, s.cfg.ForecastURL+"/health")
	checks["radiology"] = s.check(ctx, s.cfg.RadiologyURL+"/health")
	translationHTTP := strings.Replace(strings.TrimSuffix(s.cfg.TranslationURL, "/ws/translate"), "ws://", "http://", 1)
	translationHTTP = strings.Replace(translationHTTP, "wss://", "https://", 1)
	checks["translation"] = s.check(ctx, translationHTTP+"/health")
	return checks
}

func (s *server) check(ctx context.Context, endpoint string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "error"
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "offline"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ready"
	}
	return "error"
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	var input chatRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid messages"})
		s.audit(r, requestID, "/api/chat", "", http.StatusBadRequest, started)
		return
	}

	decision, err := s.route(r.Context(), input.Messages)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", "", http.StatusBadGateway, started)
		return
	}

	if decision.Type == "answer" {
		writeJSON(w, http.StatusOK, chatResponse{Answer: decision.Content, LatencyMS: time.Since(started).Milliseconds()})
		s.audit(r, requestID, "/api/chat", "", http.StatusOK, started)
		return
	}
	if decision.Type != "tool" || decision.Name == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent returned invalid decision"})
		s.audit(r, requestID, "/api/chat", "", http.StatusBadGateway, started)
		return
	}

	toolResult, err := s.executeTool(r.Context(), decision.Name, decision.Arguments)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", decision.Name, http.StatusBadGateway, started)
		return
	}

	answer, err := s.finalize(r.Context(), input.Messages, decision, toolResult)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", decision.Name, http.StatusBadGateway, started)
		return
	}

	writeJSON(w, http.StatusOK, chatResponse{Answer: answer, Tool: decision.Name, Arguments: decision.Arguments, LatencyMS: time.Since(started).Milliseconds()})
	s.audit(r, requestID, "/api/chat", decision.Name, http.StatusOK, started)
}

func (s *server) route(ctx context.Context, messages []message) (agentDecision, error) {
	tools, _ := json.Marshal(toolManifest)
	system := "You are the NDHIS local AI router. Select at most one tool. Do not invent clinical facts. Return exactly one JSON object and no markdown. For a tool call use {\"type\":\"tool\",\"name\":\"tool_name\",\"arguments\":{...}}. If no tool is needed use {\"type\":\"answer\",\"content\":\"answer\"}. Available tools: " + string(tools)
	requestMessages := append([]message{{Role: "system", Content: system}}, messages...)
	content, err := s.callAgent(ctx, requestMessages, 160)
	if err != nil {
		return agentDecision{}, err
	}
	var decision agentDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decision); err != nil {
		return agentDecision{}, fmt.Errorf("invalid agent JSON: %w", err)
	}
	return decision, nil
}

func (s *server) finalize(ctx context.Context, messages []message, decision agentDecision, result json.RawMessage) (string, error) {
	resultText := string(result)
	requestMessages := append([]message{}, messages...)
	requestMessages = append(requestMessages,
		message{Role: "assistant", Content: mustJSON(decision)},
		message{Role: "user", Content: "Tool result: " + resultText + ". Return exactly {\"type\":\"answer\",\"content\":\"concise grounded answer\"}. Do not add facts not present in the tool result."},
	)
	content, err := s.callAgent(ctx, requestMessages, 240)
	if err != nil {
		return "", err
	}
	var final agentDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &final); err != nil {
		return "", fmt.Errorf("invalid final agent JSON: %w", err)
	}
	if final.Type != "answer" || strings.TrimSpace(final.Content) == "" {
		return "", errors.New("agent did not return a final answer")
	}
	return final.Content, nil
}

func (s *server) callAgent(ctx context.Context, messages []message, maxTokens int) (string, error) {
	body, _ := json.Marshal(agentRequest{Model: s.cfg.AgentModel, Messages: messages, Temperature: 0, MaxTokens: maxTokens, ResponseFormat: map[string]string{"type": "json_object"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AgentURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("agent status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var output agentResponse
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return "", err
	}
	if len(output.Choices) == 0 {
		return "", errors.New("agent returned no choices")
	}
	return output.Choices[0].Message.Content, nil
}

func (s *server) executeTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "forecast_patient_volume":
		args["metric"] = "patient_arrivals"
		return s.postJSON(ctx, s.cfg.ForecastURL+"/forecast", args)
	case "forecast_bed_occupancy":
		args["metric"] = "bed_occupancy"
		return s.postJSON(ctx, s.cfg.ForecastURL+"/forecast", args)
	case "forecast_disease_incidence":
		args["metric"] = "disease_incidence"
		return s.postJSON(ctx, s.cfg.ForecastURL+"/forecast", args)
	case "get_radiology_result":
		id, ok := args["result_id"].(string)
		if !ok || id == "" {
			return nil, errors.New("result_id is required")
		}
		return s.getJSON(ctx, s.cfg.RadiologyURL+"/results/"+url.PathEscape(id))
	case "get_service_status":
		return json.Marshal(map[string]any{"gateway": "ready", "processing": "local", "services": s.serviceStatus(ctx)})
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *server) postJSON(ctx context.Context, endpoint string, value any) (json.RawMessage, error) {
	body, _ := json.Marshal(value)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tool status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func (s *server) getJSON(ctx context.Context, endpoint string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tool status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func (s *server) forecastProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.ForecastURL+"/forecast")
}

func (s *server) radiologyProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.RadiologyURL+"/analyze")
}

func (s *server) radiologyResultProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.RadiologyURL+"/results/"+url.PathEscape(r.PathValue("id")))
}

func (s *server) proxyHTTP(w http.ResponseWriter, r *http.Request, endpoint string) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint, r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	resp, err := s.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		s.audit(r, requestID, r.URL.Path, "", http.StatusBadGateway, started)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	s.audit(r, requestID, r.URL.Path, "", resp.StatusCode, started)
}

func (s *server) audit(r *http.Request, requestID, route, tool string, status int, started time.Time) {
	event := auditEvent{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), RequestID: requestID, User: r.Header.Get("X-NDHIS-User"), Role: r.Header.Get("X-NDHIS-Role"), Route: route, Tool: tool, Model: s.cfg.AgentModel, Status: status, LatencyMS: time.Since(started).Milliseconds()}
	payload, _ := json.Marshal(event)
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	file, err := os.OpenFile(s.cfg.AuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("audit error: %v", err)
		return
	}
	defer file.Close()
	_, _ = file.Write(append(payload, '\n'))
}

func mustJSON(value any) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
