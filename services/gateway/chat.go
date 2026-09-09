package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const routerSystemPrompt = `NDHIS local assistant/router. Return exactly one JSON object, no markdown. Keep direct answers under 80 words. Never invent clinical facts or facilities.
Prototype domain: facility JNF only; departments A&E, Outpatient, Medical Ward, Surgical Ward, Pediatrics; diseases respiratory, gastro, diabetes, hypertension. Synthetic history spans 2020-2026. Forecast horizons may use milliseconds, seconds, minutes, hours, days, weeks, months or years up to 2 years. Sub-hourly output is derived intensity/interpolation, not exact patient timing.
Use {"type":"tool","name":"NAME","arguments":{...}} or {"type":"answer","content":"..."}.
Forecast tool arguments: facility, department where applicable, disease where applicable, horizon, horizon_unit, resolution(optional), as_of(optional).
Tools: forecast_patient_volume; forecast_bed_occupancy; forecast_disease_incidence; get_radiology_result(result_id); get_service_status.`

var horizonValuePattern = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*(milliseconds?|ms|seconds?|secs?|s|minutes?|mins?|min|hours?|hrs?|hr|h|days?|d|weeks?|w|months?|mos?|mo|years?|yrs?|yr|y)\b`)
var resolutionEveryPattern = regexp.MustCompile(`(?i)\bevery\s+(\d+)\s*(milliseconds?|ms|seconds?|s|minutes?|min|hours?|h|days?|d|weeks?|w|months?|mo)\b`)
var resolutionNamedPattern = regexp.MustCompile(`(?i)\b(\d+)\s*[- ]?(millisecond|second|minute|hour|day|week|month)s?\s+(?:resolution|interval|buckets?)\b`)
var isoDatePattern = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})\b`)
var relativePastPattern = regexp.MustCompile(`(?i)\b(\d+)\s+years?\s+(?:ago|before)\b`)

type forecastContext struct {
	Active      bool
	Metric      string
	Department  string
	Disease     string
	Horizon     float64
	HorizonUnit string
	HasHorizon  bool
	Resolution  string
	AsOf        string
}

type chatStreamEvent struct {
	Type     string          `json:"type"`
	Stage    *executionStage `json:"stage,omitempty"`
	Response *chatResponse   `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type stageEmitter func(executionStage)

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	input, err := decodeChatRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", "", http.StatusBadRequest, started)
		return
	}
	response, status, err := s.runChat(r.Context(), input.Messages, requestID, nil)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		s.audit(r, requestID, "/api/chat", response.Tool, status, started)
		return
	}
	writeJSON(w, http.StatusOK, response)
	s.audit(r, requestID, "/api/chat", response.Tool, status, started)
}

func (s *server) chatStream(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	encoder := json.NewEncoder(w)
	emitEvent := func(event chatStreamEvent) {
		_ = encoder.Encode(event)
		flusher.Flush()
	}

	input, err := decodeChatRequest(r)
	if err != nil {
		emitEvent(chatStreamEvent{Type: "error", Error: err.Error()})
		s.audit(r, requestID, "/api/chat/stream", "", http.StatusBadRequest, started)
		return
	}
	emit := func(stage executionStage) { emitEvent(chatStreamEvent{Type: "stage", Stage: &stage}) }
	response, status, err := s.runChat(r.Context(), input.Messages, requestID, emit)
	if err != nil {
		emitEvent(chatStreamEvent{Type: "error", Error: err.Error()})
		s.audit(r, requestID, "/api/chat/stream", response.Tool, status, started)
		return
	}
	emitEvent(chatStreamEvent{Type: "result", Response: &response})
	s.audit(r, requestID, "/api/chat/stream", response.Tool, status, started)
}

func decodeChatRequest(r *http.Request) (chatRequest, error) {
	var input chatRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return chatRequest{}, errors.New("invalid chat request")
	}
	if err := validateMessages(input.Messages); err != nil {
		return chatRequest{}, err
	}
	return input, nil
}

func (s *server) runChat(ctx context.Context, messages []message, requestID string, emit stageEmitter) (chatResponse, int, error) {
	started := time.Now()
	trace := make([]executionStage, 0, 6)
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

	interpretStart := time.Now()
	emitStage(executionStage{Name: "interpret", Status: "running", Detail: "Resolving intent and conversation context"})
	decision, routed, intent := deterministicRouteDetailed(messages)
	complete("interpret", intentLabel(intent), time.Since(interpretStart), "", "")

	routing := "deterministic"
	llmCalls := 0
	modelUsed := ""
	if !routed {
		routing = "agent"
		llmCalls = 1
		modelUsed = s.cfg.AgentModelLabel
		agentStart := time.Now()
		emitStage(executionStage{Name: "model", Status: "running", Detail: "Calling the local conversational fallback", Model: modelUsed})
		var err error
		decision, err = s.route(ctx, messages)
		if err != nil {
			emitStage(executionStage{Name: "model", Status: "failed", Detail: err.Error(), DurationMS: time.Since(agentStart).Milliseconds(), Model: modelUsed})
			return chatResponse{RequestID: requestID, Routing: routing, Intent: "general_assistant", LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusBadGateway, err
		}
		intent = inferDecisionIntent(decision)
		complete("model", "Local model returned a bounded route/answer", time.Since(agentStart), decision.Name, modelUsed)
	} else {
		routeStage := executionStage{Name: "route", Status: "complete", Detail: "Resolved without an LLM call", Tool: decision.Name}
		trace = append(trace, routeStage)
		emitStage(routeStage)
	}

	if decision.Type == "answer" {
		return chatResponse{
			Answer: decision.Content, LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID,
			Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed,
		}, http.StatusOK, nil
	}

	validateStart := time.Now()
	emitStage(executionStage{Name: "validate", Status: "running", Detail: "Checking facility, department, horizon and resolution", Tool: decision.Name})
	validated, err := validateToolArguments(decision.Name, decision.Arguments)
	if err != nil {
		stage := executionStage{Name: "validate", Status: "failed", Detail: err.Error(), DurationMS: time.Since(validateStart).Milliseconds(), Tool: decision.Name}
		trace = append(trace, stage)
		emitStage(stage)
		return chatResponse{
			Answer: friendlyValidationFailure(err), Tool: decision.Name, Arguments: decision.Arguments,
			LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing,
			Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed,
		}, http.StatusOK, nil
	}
	decision.Arguments = validated
	complete("validate", "Inputs matched the prototype capability domain", time.Since(validateStart), decision.Name, "")

	toolStart := time.Now()
	emitStage(executionStage{Name: "tool", Status: "running", Detail: "Executing local specialist service", Tool: decision.Name})
	toolResult, err := s.executeTool(ctx, decision.Name, decision.Arguments)
	if err != nil {
		stage := executionStage{Name: "tool", Status: "failed", Detail: cleanToolError(err), DurationMS: time.Since(toolStart).Milliseconds(), Tool: decision.Name}
		trace = append(trace, stage)
		emitStage(stage)
		return chatResponse{
			Answer: friendlyToolFailure(decision.Name, err), Tool: decision.Name, Arguments: decision.Arguments,
			LatencyMS: time.Since(started).Milliseconds(), RequestID: requestID, Routing: routing,
			Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed,
		}, http.StatusOK, nil
	}
	complete("tool", "Local specialist completed", time.Since(toolStart), decision.Name, "")

	formatStart := time.Now()
	answer, err := formatToolResult(decision.Name, decision.Arguments, toolResult)
	if err != nil {
		return chatResponse{Tool: decision.Name, RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed}, http.StatusBadGateway, err
	}
	complete("format", "Formatted the tool result without another model call", time.Since(formatStart), decision.Name, "")
	return chatResponse{
		Answer: answer, Tool: decision.Name, Arguments: decision.Arguments, LatencyMS: time.Since(started).Milliseconds(),
		RequestID: requestID, Routing: routing, Intent: intent, LLMCalls: llmCalls, Trace: trace, Model: modelUsed,
	}, http.StatusOK, nil
}

func validateMessages(messages []message) error {
	if len(messages) == 0 {
		return errors.New("at least one message is required")
	}
	if len(messages) > maxChatMessages {
		return fmt.Errorf("at most %d messages are allowed", maxChatMessages)
	}
	for index, item := range messages {
		if item.Role != "user" && item.Role != "assistant" {
			return fmt.Errorf("message %d has invalid role", index+1)
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return fmt.Errorf("message %d is empty", index+1)
		}
		if len([]rune(content)) > maxMessageCharacters {
			return fmt.Errorf("message %d exceeds %d characters", index+1, maxMessageCharacters)
		}
	}
	return nil
}

func deterministicRoute(messages []message) (agentDecision, bool) {
	decision, ok, _ := deterministicRouteDetailed(messages)
	return decision, ok
}

func deterministicRouteDetailed(messages []message) (agentDecision, bool, string) {
	text := strings.ToLower(strings.TrimSpace(lastUserMessage(messages)))
	if text == "" {
		return agentDecision{}, false, ""
	}
	if serviceStatusIntent(text) {
		return agentDecision{Type: "tool", Name: "get_service_status", Arguments: map[string]any{}}, true, "service_status"
	}
	if forecastCapabilitiesIntent(text) {
		return agentDecision{Type: "answer", Content: forecastCapabilitiesAnswer()}, true, "forecast_capabilities"
	}
	if whatHappenedIntent(text) {
		if answer := explainPreviousFailure(messages); answer != "" {
			return agentDecision{Type: "answer", Content: answer}, true, "explain_previous_failure"
		}
	}

	forecast := resolveForecastContext(messages)
	if !forecast.Active {
		return agentDecision{}, false, ""
	}
	if !forecast.HasHorizon {
		return agentDecision{Type: "answer", Content: "What forecast horizon do you want? I can work from milliseconds/seconds through 2 years. For example: next 2 hours, 30 days, 6 months, or 2 years."}, true, "forecast_clarification"
	}
	if forecast.Metric == "" {
		return agentDecision{Type: "answer", Content: fmt.Sprintf("I have the %s horizon%s. Which metric do you want: patient arrivals, bed occupancy, or disease incidence?", horizonText(forecast.Horizon, forecast.HorizonUnit), departmentSuffix(forecast.Department))}, true, "forecast_clarification"
	}
	if forecast.Metric != "disease_incidence" && forecast.Department == "" {
		return agentDecision{Type: "answer", Content: "Which JNF department? Available departments are A&E, Outpatient, Medical Ward, Surgical Ward, and Pediatrics."}, true, "forecast_clarification"
	}
	if forecast.Metric == "disease_incidence" && forecast.Disease == "" {
		return agentDecision{Type: "answer", Content: "Which disease category? This synthetic prototype supports respiratory, gastro, diabetes, and hypertension incidence."}, true, "forecast_clarification"
	}

	args := map[string]any{
		"facility": "JNF", "horizon": forecast.Horizon, "horizon_unit": forecast.HorizonUnit,
		"resolution": valueOr(forecast.Resolution, "auto"),
	}
	if forecast.AsOf != "" {
		args["as_of"] = forecast.AsOf
	}
	if forecast.Department != "" {
		args["department"] = forecast.Department
	}
	switch forecast.Metric {
	case "patient_arrivals":
		return agentDecision{Type: "tool", Name: "forecast_patient_volume", Arguments: args}, true, "patient_volume_forecast"
	case "bed_occupancy":
		return agentDecision{Type: "tool", Name: "forecast_bed_occupancy", Arguments: args}, true, "bed_occupancy_forecast"
	case "disease_incidence":
		args["disease"] = forecast.Disease
		return agentDecision{Type: "tool", Name: "forecast_disease_incidence", Arguments: args}, true, "disease_incidence_forecast"
	default:
		return agentDecision{}, false, ""
	}
}

func resolveForecastContext(messages []message) forecastContext {
	start := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "user" {
			continue
		}
		text := strings.ToLower(messages[index].Content)
		if containsAny(text, "forecast", "predict", "backtest", "hindcast") {
			start = index
			break
		}
	}
	if start < 0 {
		return forecastContext{}
	}
	ctx := forecastContext{Active: true}
	for index := start; index < len(messages); index++ {
		if messages[index].Role != "user" {
			continue
		}
		applyForecastFragment(&ctx, messages[index].Content)
	}
	return ctx
}

func applyForecastFragment(ctx *forecastContext, raw string) {
	text := strings.ToLower(strings.TrimSpace(raw))
	if department := extractDepartment(text); department != "" {
		ctx.Department = department
	}
	if disease := extractDisease(text); disease != "" {
		ctx.Disease = disease
		if containsAny(text, "disease", "incidence", "cases") {
			ctx.Metric = "disease_incidence"
		}
	}
	if containsAny(text, "bed occupancy", "beds occupied", "occupancy") {
		ctx.Metric = "bed_occupancy"
	} else if containsAny(text, "patient arrival", "patient arrivals", "patient volume", "attendance", "arrivals") {
		ctx.Metric = "patient_arrivals"
	} else if containsAny(text, "disease incidence", "incidence forecast") {
		ctx.Metric = "disease_incidence"
	}
	if resolution := extractResolution(text); resolution != "" {
		ctx.Resolution = resolution
	}
	if horizon, unit, ok := extractHorizonSpec(text); ok {
		ctx.Horizon, ctx.HorizonUnit, ctx.HasHorizon = horizon, unit, true
	}
	if asOf := extractAsOf(text); asOf != "" {
		ctx.AsOf = asOf
	}
}

func extractHorizonSpec(text string) (float64, string, bool) {
	matches := horizonValuePattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		if containsAny(text, "next hour", "an hour") {
			return 1, "hours", true
		}
		return 0, "", false
	}
	// When a request says "5 minute resolution for the next 2 hours", the last
	// duration is the horizon. Otherwise the first duration is normally correct.
	match := matches[0]
	if len(matches) > 1 && containsAny(text, "resolution", "every ") {
		match = matches[len(matches)-1]
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || value <= 0 {
		return 0, "", false
	}
	unit := normalizeHorizonUnit(match[2])
	if unit == "" {
		return 0, "", false
	}
	return value, unit, true
}

func extractResolution(text string) string {
	if match := resolutionEveryPattern.FindStringSubmatch(text); len(match) == 3 {
		return resolutionCode(match[1], match[2])
	}
	if match := resolutionNamedPattern.FindStringSubmatch(text); len(match) == 3 {
		return resolutionCode(match[1], match[2])
	}
	switch {
	case containsAny(text, "millisecond-by-millisecond", "millisecond resolution"):
		return "1ms"
	case containsAny(text, "second-by-second", "per second", "second resolution"):
		return "1s"
	case containsAny(text, "minute-by-minute", "per minute", "minute resolution"):
		return "1min"
	case strings.Contains(text, "hourly"):
		return "1h"
	case strings.Contains(text, "daily"):
		return "1d"
	case strings.Contains(text, "weekly"):
		return "1w"
	case strings.Contains(text, "monthly"):
		return "1mo"
	default:
		return ""
	}
}

func resolutionCode(amount, unit string) string {
	unit = strings.ToLower(unit)
	switch {
	case strings.HasPrefix(unit, "millisecond") || unit == "ms":
		return amount + "ms"
	case strings.HasPrefix(unit, "second") || unit == "s":
		return amount + "s"
	case strings.HasPrefix(unit, "minute") || unit == "min":
		return amount + "min"
	case strings.HasPrefix(unit, "hour") || unit == "h":
		return amount + "h"
	case strings.HasPrefix(unit, "day") || unit == "d":
		return amount + "d"
	case strings.HasPrefix(unit, "week") || unit == "w":
		return amount + "w"
	case strings.HasPrefix(unit, "month") || unit == "mo":
		return amount + "mo"
	default:
		return ""
	}
}

func extractAsOf(text string) string {
	if match := isoDatePattern.FindStringSubmatch(text); len(match) == 2 && containsAny(text, "as of", "backtest", "hindcast", "historical", "from ") {
		return match[1] + "T23:59:59Z"
	}
	if match := relativePastPattern.FindStringSubmatch(text); len(match) == 2 {
		years, err := strconv.Atoi(match[1])
		if err == nil && years > 0 && years <= 6 {
			return time.Now().UTC().AddDate(-years, 0, 0).Format(time.RFC3339)
		}
	}
	return ""
}

func lastUserMessage(messages []message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return messages[index].Content
		}
	}
	return ""
}

func previousAssistantMessage(messages []message) string {
	for index := len(messages) - 2; index >= 0; index-- {
		if messages[index].Role == "assistant" {
			return messages[index].Content
		}
	}
	return ""
}

func serviceStatusIntent(text string) bool {
	return containsAny(text, "service status", "system status", "services available", "services are available", "ai services", "runtime status")
}

func forecastCapabilitiesIntent(text string) bool {
	return containsAny(text,
		"facilities and departments", "facility and department", "all facilities", "all departments",
		"available departments", "what departments", "which departments", "forecast options", "forecast capabilities",
		"what can you forecast", "forecast horizons", "forecast resolutions", "how far can you forecast",
	)
}

func whatHappenedIntent(text string) bool {
	return containsAny(text, "what happened", "what went wrong", "why did that fail", "why did it fail")
}

func explainPreviousFailure(messages []message) string {
	previous := strings.ToLower(previousAssistantMessage(messages))
	switch {
	case containsAny(previous, "no synthetic data", "unsupported department", "selection"):
		return "The previous forecast could not match the requested selection to the synthetic dataset. This prototype currently has JNF with A&E, Outpatient, Medical Ward, Surgical Ward, and Pediatrics. I can retry once those inputs match the supported domain."
	case containsAny(previous, "radiology", "analysis unavailable"):
		return "The radiology request failed in the local vision service before a usable result was produced. The execution trace identifies the failing stage; the output should not be treated as a radiology finding."
	case previous != "":
		return "The previous request did not produce a successful specialist result. Open its execution trace to see whether interpretation, validation, model routing, or the local tool failed."
	default:
		return "I do not have a previous assistant result in this chat to explain."
	}
}

func forecastCapabilitiesAnswer() string {
	return "Forecasting uses synthetic JNF operational history from 2020 through 2026. Facility: JNF. Departments: A&E, Outpatient, Medical Ward, Surgical Ward, Pediatrics. Metrics: patient arrivals, bed occupancy, disease incidence. Horizons can range from milliseconds/seconds to 2 years, with bounded output points. Sub-hourly values are derived event intensity/interpolation from hourly source data, not exact patient timestamps."
}

func extractDepartment(text string) string {
	switch {
	case containsAny(text, "a&e", "a & e", "emergency department", "emergency room"):
		return "A&E"
	case strings.Contains(text, "outpatient"):
		return "Outpatient"
	case strings.Contains(text, "medical ward"):
		return "Medical Ward"
	case strings.Contains(text, "surgical ward"):
		return "Surgical Ward"
	case containsAny(text, "pediatrics", "paediatrics", "pediatric", "paediatric"):
		return "Pediatrics"
	default:
		return ""
	}
}

func extractDisease(text string) string {
	for _, disease := range supportedDiseases {
		if strings.Contains(text, disease) {
			return disease
		}
	}
	return ""
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func departmentSuffix(department string) string {
	if department == "" {
		return ""
	}
	return " for " + department
}

func horizonText(value float64, unit string) string {
	formatted := strconv.FormatFloat(value, 'f', -1, 64)
	return formatted + " " + unit
}

func intentLabel(intent string) string {
	if intent == "" {
		return "No deterministic specialist intent matched"
	}
	return strings.ReplaceAll(intent, "_", " ")
}

func inferDecisionIntent(decision agentDecision) string {
	if decision.Type == "tool" {
		return decision.Name
	}
	return "general_assistant"
}

func friendlyValidationFailure(err error) string {
	return "I couldn't run that request because the inputs are outside the prototype's supported domain: " + err.Error() + "."
}

func cleanToolError(err error) string {
	var toolErr *toolHTTPError
	if errors.As(err, &toolErr) {
		return toolErr.Detail
	}
	return err.Error()
}

func friendlyToolFailure(name string, err error) string {
	detail := cleanToolError(err)
	if strings.HasPrefix(name, "forecast_") {
		return "I couldn't run that forecast: " + detail + ". JNF departments are A&E, Outpatient, Medical Ward, Surgical Ward, and Pediatrics; supported horizons run up to 2 years."
	}
	if name == "get_radiology_result" {
		return "The local radiology service could not return that result: " + detail + ". Clinician review remains required; no finding was produced from this failure."
	}
	return "The local specialist service could not complete the request: " + detail + "."
}

func (s *server) route(ctx context.Context, messages []message) (agentDecision, error) {
	requestMessages := []message{{Role: "system", Content: routerSystemPrompt}}
	requestMessages = append(requestMessages, compactRoutingHistory(messages)...)
	content, err := s.callAgent(ctx, requestMessages, s.cfg.AgentMaxTokens)
	if err != nil {
		return agentDecision{}, err
	}
	decision, err := parseAgentDecision(content)
	if err != nil {
		return agentDecision{}, err
	}
	return validateDecision(decision)
}

func compactRoutingHistory(messages []message) []message {
	start := 0
	if len(messages) > maxRoutingHistory {
		start = len(messages) - maxRoutingHistory
	}
	result := make([]message, 0, len(messages)-start)
	for _, item := range messages[start:] {
		content := strings.TrimSpace(item.Content)
		if len([]rune(content)) > 1200 {
			content = string([]rune(content)[:1200])
		}
		result = append(result, message{Role: item.Role, Content: content})
	}
	return result
}

func parseAgentDecision(content string) (agentDecision, error) {
	trimmed := strings.TrimSpace(content)
	start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return agentDecision{}, errors.New("agent returned no JSON object")
	}
	var decision agentDecision
	decoder := json.NewDecoder(strings.NewReader(trimmed[start : end+1]))
	if err := decoder.Decode(&decision); err != nil {
		return agentDecision{}, fmt.Errorf("invalid agent JSON: %w", err)
	}
	return decision, nil
}

func validateDecision(decision agentDecision) (agentDecision, error) {
	switch decision.Type {
	case "answer":
		decision.Content = strings.TrimSpace(decision.Content)
		if decision.Content == "" {
			return agentDecision{}, errors.New("agent answer is empty")
		}
		if len([]rune(decision.Content)) > 2000 {
			return agentDecision{}, errors.New("agent answer exceeds output limit")
		}
		decision.Name, decision.Arguments = "", nil
		return decision, nil
	case "tool":
		if _, ok := allowedTools[decision.Name]; !ok {
			return agentDecision{}, fmt.Errorf("agent selected unsupported tool: %s", decision.Name)
		}
		decision.Content = ""
		return decision, nil
	default:
		return agentDecision{}, errors.New("agent returned invalid decision type")
	}
}

func formatToolResult(name string, args map[string]any, result json.RawMessage) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		return "", fmt.Errorf("invalid tool result: %w", err)
	}

	switch name {
	case "forecast_patient_volume", "forecast_bed_occupancy", "forecast_disease_incidence":
		expected, okExpected := number(payload["expected"])
		p10, okP10 := number(payload["p10"])
		p90, okP90 := number(payload["p90"])
		if !okExpected || !okP10 || !okP90 {
			return compactToolFallback(name, payload), nil
		}
		label := forecastHorizonLabel(payload, args)
		resolution, _ := payload["resolution"].(string)
		if resolution == "" {
			resolution = stringArg(args, "resolution")
		}
		var answer string
		if name == "forecast_disease_incidence" {
			answer = fmt.Sprintf("%s %s incidence forecast for JNF: expected %s, P10-P90 %s-%s", label, stringArg(args, "disease"), formatNumber(expected), formatNumber(p10), formatNumber(p90))
		} else {
			metric := "patient arrivals"
			if name == "forecast_bed_occupancy" {
				metric = "bed occupancy"
			}
			answer = fmt.Sprintf("%s %s forecast for %s at JNF: expected %s, P10-P90 %s-%s", label, metric, stringArg(args, "department"), formatNumber(expected), formatNumber(p10), formatNumber(p90))
		}
		if resolution != "" {
			answer += "; resolution " + resolution
		}
		if intensity, ok := payload["intensity"].(map[string]any); ok && intensity != nil {
			if perHour, ok := number(intensity["expected_per_hour"]); ok {
				answer += fmt.Sprintf(". Current derived event intensity is about %s/hour", formatNumber(perHour))
			}
		}
		if backtest, ok := payload["backtest"].(map[string]any); ok {
			if available, _ := backtest["available"].(bool); available {
				if mae, ok := number(backtest["mae"]); ok {
					answer += fmt.Sprintf(". Historical backtest MAE: %s", formatNumber(mae))
				}
			}
		}
		return answer + ". Synthetic operational prototype data; sub-hourly values are rates/interpolations, not exact patient timestamps.", nil

	case "get_radiology_result":
		findings, _ := payload["findings"].(string)
		findings = strings.TrimSpace(findings)
		if findings == "" {
			return compactToolFallback(name, payload), nil
		}
		resultID, _ := payload["result_id"].(string)
		if resultID == "" {
			resultID = stringArg(args, "result_id")
		}
		return fmt.Sprintf("Radiology result %s: %s Clinician review is required before any clinical use.", resultID, findings), nil

	case "get_service_status":
		services, _ := payload["services"].(map[string]any)
		if len(services) == 0 {
			return "The local NDHIS AI gateway is responding, but no specialist service states were returned.", nil
		}
		keys := make([]string, 0, len(services))
		for key := range services {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		states := make([]string, 0, len(keys))
		for _, key := range keys {
			states = append(states, fmt.Sprintf("%s: %v", key, services[key]))
		}
		return "Local AI service status — " + strings.Join(states, "; ") + ".", nil
	default:
		return "", fmt.Errorf("unsupported tool result: %s", name)
	}
}

func forecastHorizonLabel(payload map[string]any, args map[string]any) string {
	if horizon, ok := payload["horizon"].(map[string]any); ok {
		value, valueOK := number(horizon["value"])
		unit, _ := horizon["unit"].(string)
		if valueOK && unit != "" {
			return horizonText(value, unit)
		}
	}
	if value, ok := numberAsFloat(args["horizon"]); ok {
		return horizonText(value, stringArg(args, "horizon_unit"))
	}
	if days, ok := numberAsInt(args["horizon_days"]); ok {
		return fmt.Sprintf("%d days", days)
	}
	return "Forecast"
}

func compactToolFallback(name string, payload map[string]any) string {
	encoded, _ := json.Marshal(payload)
	text := string(encoded)
	if len(text) > 1200 {
		text = text[:1200] + "…"
	}
	return fmt.Sprintf("%s completed successfully. Result: %s", name, text)
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func number(value any) (float64, bool) {
	return numberAsFloat(value)
}

func formatNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	magnitude := value
	if magnitude < 0 {
		magnitude = -magnitude
	}
	if magnitude > 0 && magnitude < 0.01 {
		return strconv.FormatFloat(value, 'f', 6, 64)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func (s *server) callAgent(ctx context.Context, messages []message, maxTokens int) (string, error) {
	if maxTokens <= 0 || maxTokens > s.cfg.AgentMaxTokens {
		maxTokens = s.cfg.AgentMaxTokens
	}
	body, err := json.Marshal(agentRequest{
		Model:              s.cfg.AgentModel,
		Messages:           messages,
		Temperature:        0,
		MaxTokens:          maxTokens,
		ResponseFormat:     map[string]string{"type": "json_object"},
		CachePrompt:        true,
		ReasoningEffort:    "none",
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
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
