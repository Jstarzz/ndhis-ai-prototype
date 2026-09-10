package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const operationalRouterPromptV2 = `NDHIS operational tool router. Return one compact JSON object only. Do not answer general questions. Route only supported operational actions or ask one short clarification.
Facility is JNF. Departments: A&E, Outpatient, Medical Ward, Surgical Ward, Pediatrics. Diseases: respiratory, gastro, diabetes, hypertension. Forecast horizon: milliseconds through 2 years. Tools: forecast_patient_volume, forecast_bed_occupancy, forecast_disease_incidence, get_radiology_result, get_service_status.
Return {"t":"tool","n":"TOOL","a":{...}} or {"t":"clarify","c":"short question"}. Omit facility; the gateway inserts JNF. Never invent unsupported values.`

const generalAssistantPromptV2 = `You are NDHIS AI, a concise local clinical-operations prototype for JNF General Hospital. Speak naturally rather than like a rule-based bot. You can explain the prototype, local inference, synthetic forecasting, translation, radiology screening, performance and system behavior. Never invent patient data, facilities, diagnoses or tool results. Do not claim access to production NDHIS records. Radiology is research screening with clinician review, not autonomous diagnosis. Forecast operational data is synthetic and spans 2020-2026. If a clinical diagnosis or treatment decision is requested, clearly defer to a qualified clinician.`

var radiologyResultIDPatternV2 = regexp.MustCompile(`(?i)\b[0-9a-f]{16}\b`)
var plusHalfDurationPatternV2 = regexp.MustCompile(`(?i)\b(?:(\d+(?:\.\d+)?|one|two|three|four|five|six|seven|eight|nine|ten|a|an)\s+)?(millisecond|second|minute|hour|day|week|month|year)s?\s+and\s+(?:a\s+)?half\b`)
var halfDurationPatternV2 = regexp.MustCompile(`(?i)\bhalf\s+(?:(?:a|an)\s+)?(millisecond|second|minute|hour|day|week|month|year)s?\b`)
var spokenDurationPatternV2 = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|seven|eight|nine|ten|a|an)\s+(millisecond|second|minute|hour|day|week|month|year)s?\b`)
var routingTextReplacerV2 = strings.NewReplacer(
	"a and e", "a&e",
	"a & e", "a&e",
	"a / e", "a&e",
	"a/e", "a&e",
	"a n e", "a&e",
	"emergency dept", "emergency department",
	"er department", "emergency department",
)

type chatStreamEventV2 struct {
	Type     string          `json:"type"`
	Stage    *executionStage `json:"stage,omitempty"`
	Response *chatResponse   `json:"response,omitempty"`
	Delta    string          `json:"delta,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type deltaEmitterV2 func(string)

type compactRouterDecisionV2 struct {
	Type      string         `json:"t"`
	Name      string         `json:"n,omitempty"`
	Arguments map[string]any `json:"a,omitempty"`
	Content   string         `json:"c,omitempty"`
}

type agentRequestV2 struct {
	Model              string         `json:"model"`
	Messages           []message      `json:"messages"`
	Temperature        float64        `json:"temperature"`
	MaxTokens          int            `json:"max_tokens"`
	ResponseFormat     map[string]any `json:"response_format,omitempty"`
	CachePrompt        bool           `json:"cache_prompt"`
	Stream             bool           `json:"stream,omitempty"`
	ReasoningEffort    string         `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type agentStreamChunkV2 struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func (s *server) chatV2(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	input, err := decodeChatRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", "", http.StatusBadRequest, started)
		return
	}
	response, status, err := s.runChatV2(r.Context(), input.Messages, requestID, nil, nil)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", response.Tool, status, started)
		return
	}
	writeJSON(w, http.StatusOK, response)
	s.audit(r, requestID, "/api/chat", response.Tool, status, started)
}

func (s *server) chatStreamV2(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("Content-Encoding", "identity")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	encoder := json.NewEncoder(w)
	emitEvent := func(event chatStreamEventV2) {
		_ = encoder.Encode(event)
		flusher.Flush()
	}
	input, err := decodeChatRequest(r)
	if err != nil {
		emitEvent(chatStreamEventV2{Type: "error", Error: err.Error()})
		s.audit(r, requestID, "/api/chat/stream", "", http.StatusBadRequest, started)
		return
	}
	emitStage := func(stage executionStage) { emitEvent(chatStreamEventV2{Type: "stage", Stage: &stage}) }
	emitDelta := func(delta string) { emitEvent(chatStreamEventV2{Type: "delta", Delta: delta}) }
	response, status, err := s.runChatV2(r.Context(), input.Messages, requestID, emitStage, emitDelta)
	if err != nil {
		emitEvent(chatStreamEventV2{Type: "error", Error: err.Error()})
		s.audit(r, requestID, "/api/chat/stream", response.Tool, status, started)
		return
	}
	emitEvent(chatStreamEventV2{Type: "result", Response: &response})
	s.audit(r, requestID, "/api/chat/stream", response.Tool, status, started)
}

func (s *server) runChatV2(ctx context.Context, messages []message, requestID string, emit stageEmitter, emitDelta deltaEmitterV2) (chatResponse, int, error) {
	started := time.Now()
	trace := make([]executionStage, 0, 7)
	emitStage := func(stage executionStage) {
		if emit != nil {
			emit(stage)
		}
	}
	complete := func(name, detail string, duration time.Duration, tool, model string) {
		stage := executionStage{Name: name, Status: "complete", Detail: detail, DurationMS: duration.Milliseconds(), Tool: tool, Model: model}
		trace = append(trace, stage)
		emitStage(stage)
	}
	failed := func(name, detail string, duration time.Duration, tool, model string) {
		stage := executionStage{Name: name, Status: "failed", Detail: detail, DurationMS: duration.Milliseconds(), Tool: tool, Model: model}
		trace = append(trace, stage)
		emitStage(stage)
	}

	routingMessages := normalizeMessagesV2(messages)
	interpretStart := time.Now()
	emitStage(executionStage{Name: "interpret", Status: "running", Detail: "Resolving language, intent and conversation state"})
	decision, routed, intent := directRouteV2(routingMessages)
	complete("interpret", intentLabel(intent), time.Since(interpretStart), "", "")

	routing := "deterministic"
	llmCalls := 0
	modelUsed := ""
	if !routed {
		modelUsed = s.cfg.AgentModelLabel
		llmCalls = 1
		modelStart := time.Now()
		if operationalCandidateV2(routingMessages) {
			routing = "agent-router"
			emitStage(executionStage{Name: "model", Status: "running", Detail: "Resolving an ambiguous operational request with the constrained local router", Model: modelUsed})
			var err error
			decision, err = s.routeOperationalV2(ctx, routingMessages)
			if err != nil {
				failed("model", sanitizeRouterErrorV2(err), time.Since(modelStart), "", modelUsed)
				answer := "I couldn't safely map that request to a supported local tool. Try specifying the JNF department, metric and time horizon, or ask the question conversationally without requesting an action."
				return chatResponse{Answer: answer, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing, Intent: "operational_clarification", LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusOK, nil
			}
			if decision.Type == "answer" {
				intent = "operational_clarification"
			} else {
				intent = inferDecisionIntent(decision)
			}
			complete("model", "Constrained local router returned a bounded decision", time.Since(modelStart), decision.Name, modelUsed)
		} else {
			routing = "assistant"
			intent = "general_assistant"
			emitStage(executionStage{Name: "model", Status: "running", Detail: "Generating a conversational response locally", Model: modelUsed})
			answer, err := s.answerGeneralV2(ctx, messages, emitDelta)
			if err != nil {
				failed("model", err.Error(), time.Since(modelStart), "", modelUsed)
				return chatResponse{RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusBadGateway, err
			}
			complete("model", "Local conversational response completed", time.Since(modelStart), "", modelUsed)
			return chatResponse{Answer: answer, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusOK, nil
		}
	} else {
		routeStage := executionStage{Name: "route", Status: "complete", Detail: "Resolved without an LLM call", Tool: decision.Name}
		trace = append(trace, routeStage)
		emitStage(routeStage)
	}

	if decision.Type == "answer" {
		return chatResponse{Answer: decision.Content, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusOK, nil
	}

	validateStart := time.Now()
	emitStage(executionStage{Name: "validate", Status: "running", Detail: "Checking tool arguments against the local capability domain", Tool: decision.Name})
	validated, err := validateToolArguments(decision.Name, decision.Arguments)
	if err != nil {
		failed("validate", err.Error(), time.Since(validateStart), decision.Name, "")
		return chatResponse{Answer: friendlyValidationFailure(err), Tool: decision.Name, Arguments: decision.Arguments, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusOK, nil
	}
	decision.Arguments = validated
	complete("validate", "Inputs matched the prototype capability domain", time.Since(validateStart), decision.Name, "")

	toolStart := time.Now()
	emitStage(executionStage{Name: "tool", Status: "running", Detail: "Executing local specialist service", Tool: decision.Name})
	toolResult, err := s.executeTool(ctx, decision.Name, decision.Arguments)
	if err != nil {
		failed("tool", cleanToolError(err), time.Since(toolStart), decision.Name, "")
		return chatResponse{Answer: friendlyToolFailure(decision.Name, err), Tool: decision.Name, Arguments: decision.Arguments, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusOK, nil
	}
	complete("tool", "Local specialist completed", time.Since(toolStart), decision.Name, "")

	formatStart := time.Now()
	answer, err := formatToolResult(decision.Name, decision.Arguments, toolResult)
	if err != nil {
		failed("format", err.Error(), time.Since(formatStart), decision.Name, "")
		return chatResponse{Tool: decision.Name, RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusBadGateway, err
	}
	complete("format", "Formatted the specialist result without another model call", time.Since(formatStart), decision.Name, "")
	return chatResponse{Answer: answer, Tool: decision.Name, Arguments: decision.Arguments, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusOK, nil
}

func normalizeMessagesV2(messages []message) []message {
	result := make([]message, len(messages))
	for index, item := range messages {
		result[index] = item
		if item.Role == "user" {
			result[index].Content = normalizeConversationalTextV2(item.Content)
		}
	}
	return result
}

func normalizeConversationalTextV2(raw string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	text = routingTextReplacerV2.Replace(text)
	text = plusHalfDurationPatternV2.ReplaceAllStringFunc(text, func(match string) string {
		parts := plusHalfDurationPatternV2.FindStringSubmatch(match)
		base := 1.0
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			if parsed, ok := spokenNumberV2(parts[1]); ok {
				base = parsed
			}
		}
		unit := "minutes"
		if len(parts) > 2 {
			unit = pluralDurationUnitV2(parts[2])
		}
		return strconv.FormatFloat(base+0.5, 'f', -1, 64) + " " + unit
	})
	text = halfDurationPatternV2.ReplaceAllStringFunc(text, func(match string) string {
		parts := halfDurationPatternV2.FindStringSubmatch(match)
		unit := "minutes"
		if len(parts) > 1 {
			unit = pluralDurationUnitV2(parts[1])
		}
		return "0.5 " + unit
	})
	text = spokenDurationPatternV2.ReplaceAllStringFunc(text, func(match string) string {
		parts := spokenDurationPatternV2.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		value, ok := spokenNumberV2(parts[1])
		if !ok {
			return match
		}
		return strconv.FormatFloat(value, 'f', -1, 64) + " " + pluralDurationUnitV2(parts[2])
	})
	if containsAny(text, "a&e", "emergency department", "outpatient", "medical ward", "surgical ward", "pediatrics", "paediatrics", "pediatric", "paediatric") && strings.Contains(text, "patients") {
		text = strings.ReplaceAll(text, "patients", "patient arrivals")
	}
	if strings.Contains(text, "surgical patient arrivals") {
		text = strings.ReplaceAll(text, "surgical patient arrivals", "surgical ward patient arrivals")
	}
	return strings.Join(strings.Fields(text), " ")
}

func spokenNumberV2(value string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "a", "an", "one":
		return 1, true
	case "two":
		return 2, true
	case "three":
		return 3, true
	case "four":
		return 4, true
	case "five":
		return 5, true
	case "six":
		return 6, true
	case "seven":
		return 7, true
	case "eight":
		return 8, true
	case "nine":
		return 9, true
	case "ten":
		return 10, true
	default:
		parsed, err := strconv.ParseFloat(value, 64)
		return parsed, err == nil
	}
}

func pluralDurationUnitV2(value string) string {
	unit := strings.ToLower(strings.TrimSpace(value))
	if strings.HasSuffix(unit, "s") {
		return unit
	}
	return unit + "s"
}

func directRouteV2(messages []message) (agentDecision, bool, string) {
	text := strings.ToLower(strings.TrimSpace(lastUserMessage(messages)))
	if assistantCapabilitiesIntentV2(text) {
		return agentDecision{Type: "answer", Content: assistantCapabilitiesAnswerV2()}, true, "assistant_capabilities"
	}
	if match := radiologyResultIDPatternV2.FindString(text); match != "" {
		return agentDecision{Type: "tool", Name: "get_radiology_result", Arguments: map[string]any{"result_id": match}}, true, "radiology_result"
	}
	return deterministicRouteDetailed(messages)
}

func assistantCapabilitiesIntentV2(text string) bool {
	return containsAny(text, "what does this do", "what can you do", "what are you", "what is ndhis ai", "what is this prototype", "explain this prototype", "what can the assistant do", "how does this prototype work")
}

func assistantCapabilitiesAnswerV2() string {
	return "NDHIS AI is a local JNF clinical-operations prototype. It can forecast synthetic patient demand from 2020-2026, run local chest-X-ray research screening, support live translation, report service health, and use a small local language model for general questions. Common operational requests bypass the LLM for speed. It does not provide autonomous diagnosis or treatment decisions."
}

func operationalCandidateV2(messages []message) bool {
	if forecast := resolveForecastContext(messages); forecast.Active {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(lastUserMessage(messages)))
	if radiologyResultIDPatternV2.MatchString(text) {
		return true
	}
	action := containsAny(text, "forecast", "predict", "run ", "show ", "get ", "retrieve", "check ", "gimme", "give me", "tell me")
	domain := containsAny(text, "patient", "arrival", "bed", "occupancy", "incidence", "radiology", "x-ray", "xray", "screening", "service status", "a&e", "emergency", "outpatient", "ward", "pediatric")
	if action && domain {
		return true
	}
	future := containsAny(text, "next", "coming", "later", "right now") && containsAny(text, "minute", "hour", "day", "week", "month", "year")
	return future && domain
}

func routingContextV2(messages []message) string {
	parts := []string{"user=" + strconv.Quote(truncateRunesV2(lastUserMessage(messages), 700))}
	forecast := resolveForecastContext(messages)
	if forecast.Active {
		state := []string{"forecast=active"}
		if forecast.Metric != "" {
			state = append(state, "metric="+forecast.Metric)
		}
		if forecast.Department != "" {
			state = append(state, "department="+forecast.Department)
		}
		if forecast.Disease != "" {
			state = append(state, "disease="+forecast.Disease)
		}
		if forecast.HasHorizon {
			state = append(state, "horizon="+horizonText(forecast.Horizon, forecast.HorizonUnit))
		}
		if forecast.Resolution != "" {
			state = append(state, "resolution="+forecast.Resolution)
		}
		if forecast.AsOf != "" {
			state = append(state, "as_of="+forecast.AsOf)
		}
		parts = append(parts, strings.Join(state, ";"))
	}
	if previous := strings.TrimSpace(previousAssistantMessage(messages)); previous != "" {
		parts = append(parts, "previous_assistant="+strconv.Quote(truncateRunesV2(previous, 220)))
	}
	return strings.Join(parts, "\n")
}

func truncateRunesV2(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func (s *server) routeOperationalV2(ctx context.Context, messages []message) (agentDecision, error) {
	requestMessages := []message{{Role: "system", Content: operationalRouterPromptV2}, {Role: "user", Content: routingContextV2(messages)}}
	content, err := s.callAgentV2(ctx, requestMessages, routerTokenLimitV2(s), 0, routerResponseFormatV2(), false, nil)
	if err != nil {
		return agentDecision{}, err
	}
	trimmed := strings.TrimSpace(content)
	start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return agentDecision{}, errors.New("router returned no JSON object")
	}
	var compact compactRouterDecisionV2
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &compact); err != nil {
		return agentDecision{}, fmt.Errorf("invalid router JSON: %w", err)
	}
	switch compact.Type {
	case "clarify":
		content := strings.TrimSpace(compact.Content)
		if content == "" {
			content = "Which supported JNF department, metric and time horizon should I use?"
		}
		return agentDecision{Type: "answer", Content: content}, nil
	case "tool":
		if _, ok := allowedTools[compact.Name]; !ok {
			return agentDecision{}, fmt.Errorf("router selected unsupported tool: %s", compact.Name)
		}
		if compact.Arguments == nil {
			compact.Arguments = map[string]any{}
		}
		if strings.HasPrefix(compact.Name, "forecast_") {
			compact.Arguments["facility"] = "JNF"
		}
		return agentDecision{Type: "tool", Name: compact.Name, Arguments: compact.Arguments}, nil
	default:
		return agentDecision{}, errors.New("router returned invalid decision type")
	}
}

func routerResponseFormatV2() map[string]any {
	return map[string]any{
		"type": "json_schema",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"t": map[string]any{"type": "string", "enum": []string{"tool", "clarify"}},
				"n": map[string]any{"type": "string", "enum": []string{"forecast_patient_volume", "forecast_bed_occupancy", "forecast_disease_incidence", "get_radiology_result", "get_service_status"}},
				"c": map[string]any{"type": "string", "maxLength": 180},
				"a": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"department":   map[string]any{"type": "string", "enum": supportedDepartments},
						"disease":      map[string]any{"type": "string", "enum": supportedDiseases},
						"horizon":      map[string]any{"type": "number", "exclusiveMinimum": 0},
						"horizon_unit": map[string]any{"type": "string", "enum": []string{"milliseconds", "seconds", "minutes", "hours", "days", "weeks", "months", "years"}},
						"resolution":   map[string]any{"type": "string"},
						"as_of":        map[string]any{"type": "string"},
						"result_id":    map[string]any{"type": "string"},
					},
					"additionalProperties": false,
				},
			},
			"required":             []string{"t"},
			"additionalProperties": false,
		},
	}
}

func routerTokenLimitV2(s *server) int {
	limit := optionalIntEnv("AGENT_ROUTER_MAX_TOKENS", 40)
	if s.cfg.AgentMaxTokens > 0 && limit > s.cfg.AgentMaxTokens {
		return s.cfg.AgentMaxTokens
	}
	return limit
}

func assistantTokenLimitV2(s *server) int {
	limit := optionalIntEnv("AGENT_ASSISTANT_MAX_TOKENS", 96)
	if s.cfg.AgentMaxTokens > 0 && limit > s.cfg.AgentMaxTokens {
		return s.cfg.AgentMaxTokens
	}
	return limit
}

func (s *server) answerGeneralV2(ctx context.Context, messages []message, emitDelta deltaEmitterV2) (string, error) {
	requestMessages := []message{{Role: "system", Content: generalAssistantPromptV2}}
	requestMessages = append(requestMessages, compactAssistantHistoryV2(messages)...)
	return s.callAgentV2(ctx, requestMessages, assistantTokenLimitV2(s), 0.2, nil, emitDelta != nil, emitDelta)
}

func compactAssistantHistoryV2(messages []message) []message {
	start := 0
	if len(messages) > 4 {
		start = len(messages) - 4
	}
	result := make([]message, 0, len(messages)-start)
	for _, item := range messages[start:] {
		result = append(result, message{Role: item.Role, Content: truncateRunesV2(item.Content, 900)})
	}
	return result
}

func (s *server) callAgentV2(ctx context.Context, messages []message, maxTokens int, temperature float64, responseFormat map[string]any, stream bool, emitDelta deltaEmitterV2) (string, error) {
	if maxTokens <= 0 || maxTokens > s.cfg.AgentMaxTokens {
		maxTokens = s.cfg.AgentMaxTokens
	}
	requestBody := agentRequestV2{Model: s.cfg.AgentModel, Messages: messages, Temperature: temperature, MaxTokens: maxTokens, ResponseFormat: responseFormat, CachePrompt: true, Stream: stream, ReasoningEffort: "none", ChatTemplateKwargs: map[string]any{"enable_thinking": false}}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}
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
	if !stream {
		var output agentResponse
		decoder := json.NewDecoder(io.LimitReader(resp.Body, maxAgentResponse))
		if err := decoder.Decode(&output); err != nil {
			return "", fmt.Errorf("invalid agent response: %w", err)
		}
		if len(output.Choices) == 0 {
			return "", errors.New("agent returned no choices")
		}
		content := strings.TrimSpace(output.Choices[0].Message.Content)
		if content == "" {
			return "", errors.New("agent returned empty content")
		}
		return content, nil
	}

	var builder strings.Builder
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxAgentResponse))
	scanner.Buffer(make([]byte, 4096), maxAgentResponse)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk agentStreamChunkV2
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return "", fmt.Errorf("invalid agent stream chunk: %w", err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		builder.WriteString(delta)
		if emitDelta != nil {
			emitDelta(delta)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("agent stream failed: %w", err)
	}
	content := strings.TrimSpace(builder.String())
	if content == "" {
		return "", errors.New("agent stream returned no content")
	}
	return content, nil
}

func sanitizeRouterErrorV2(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 260 {
		text = text[:260] + "…"
	}
	return text
}

func (s *server) warmAgentV2() {
	time.Sleep(2 * time.Second)
	for attempt := 0; attempt < 6; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		_, routeErr := s.routeOperationalV2(ctx, []message{{Role: "user", Content: "Check service status."}})
		cancel()
		if routeErr == nil {
			ctxAssistant, cancelAssistant := context.WithTimeout(context.Background(), 30*time.Second)
			_, assistantErr := s.callAgentV2(ctxAssistant, []message{{Role: "system", Content: generalAssistantPromptV2}, {Role: "user", Content: "Reply OK."}}, 4, 0, nil, false, nil)
			cancelAssistant()
			if assistantErr == nil {
				fmt.Println("agent warm-up complete")
				return
			}
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	fmt.Println("agent warm-up incomplete; requests will retry normally")
}
