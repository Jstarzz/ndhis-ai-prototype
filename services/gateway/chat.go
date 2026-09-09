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

const routerSystemPrompt = `NDHIS local router. Return one JSON object only. Never invent clinical facts.
Use {"type":"tool","name":"NAME","arguments":{...}} or {"type":"answer","content":"..."}.
Tools: forecast_patient_volume(facility,department,horizon_days); forecast_bed_occupancy(facility,department,horizon_days); forecast_disease_incidence(facility,disease,horizon_days); get_radiology_result(result_id); get_service_status().`

var horizonPattern = regexp.MustCompile(`\b(\d{1,2})\s*(?:day|days|d)\b`)

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
    started := time.Now()
    requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
    w.Header().Set("X-NDHIS-Request-ID", requestID)

    var input chatRequest
    decoder := json.NewDecoder(r.Body)
    decoder.DisallowUnknownFields()
    if err := decoder.Decode(&input); err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid chat request"})
        s.audit(r, requestID, "/api/chat", "", http.StatusBadRequest, started)
        return
    }
    if err := validateMessages(input.Messages); err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
        s.audit(r, requestID, "/api/chat", "", http.StatusBadRequest, started)
        return
    }

    decision, routed := deterministicRoute(input.Messages)
    routing := "deterministic"
    llmCalls := 0
    if !routed {
        routing = "agent"
        llmCalls = 1
        var err error
        decision, err = s.route(r.Context(), input.Messages)
        if err != nil {
            writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
            s.audit(r, requestID, "/api/chat", "", http.StatusBadGateway, started)
            return
        }
    }

    if decision.Type == "answer" {
        writeJSON(w, http.StatusOK, chatResponse{
            Answer: decision.Content,
            LatencyMS: time.Since(started).Milliseconds(),
            RequestID: requestID,
            Routing: routing,
            LLMCalls: llmCalls,
        })
        s.audit(r, requestID, "/api/chat", "", http.StatusOK, started)
        return
    }

    toolResult, err := s.executeTool(r.Context(), decision.Name, decision.Arguments)
    if err != nil {
        writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
        s.audit(r, requestID, "/api/chat", decision.Name, http.StatusBadGateway, started)
        return
    }

    answer, err := formatToolResult(decision.Name, decision.Arguments, toolResult)
    if err != nil {
        writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
        s.audit(r, requestID, "/api/chat", decision.Name, http.StatusBadGateway, started)
        return
    }

    writeJSON(w, http.StatusOK, chatResponse{
        Answer: answer,
        Tool: decision.Name,
        Arguments: decision.Arguments,
        LatencyMS: time.Since(started).Milliseconds(),
        RequestID: requestID,
        Routing: routing,
        LLMCalls: llmCalls,
    })
    s.audit(r, requestID, "/api/chat", decision.Name, http.StatusOK, started)
}

func validateMessages(messages []message) error {
    if len(messages) == 0 { return errors.New("at least one message is required") }
    if len(messages) > maxChatMessages { return fmt.Errorf("at most %d messages are allowed", maxChatMessages) }
    for index, item := range messages {
        if item.Role != "user" && item.Role != "assistant" { return fmt.Errorf("message %d has invalid role", index+1) }
        content := strings.TrimSpace(item.Content)
        if content == "" { return fmt.Errorf("message %d is empty", index+1) }
        if len([]rune(content)) > maxMessageCharacters { return fmt.Errorf("message %d exceeds %d characters", index+1, maxMessageCharacters) }
    }
    return nil
}

func deterministicRoute(messages []message) (agentDecision, bool) {
    text := strings.ToLower(strings.TrimSpace(lastUserMessage(messages)))
    if text == "" { return agentDecision{}, false }

    if serviceStatusIntent(text) {
        return agentDecision{Type: "tool", Name: "get_service_status", Arguments: map[string]any{}}, true
    }

    if !strings.Contains(text, "forecast") { return agentDecision{}, false }
    days, ok := extractHorizon(text)
    if !ok { return agentDecision{}, false }

    facility := "JNF"
    if department := extractDepartment(text); department != "" {
        switch {
        case containsAny(text, "bed occupancy", "beds occupied", "occupancy"):
            return agentDecision{Type: "tool", Name: "forecast_bed_occupancy", Arguments: map[string]any{"facility": facility, "department": department, "horizon_days": days}}, true
        case containsAny(text, "patient arrival", "patient arrivals", "patient volume", "patients", "attendance"):
            return agentDecision{Type: "tool", Name: "forecast_patient_volume", Arguments: map[string]any{"facility": facility, "department": department, "horizon_days": days}}, true
        }
    }

    if disease := extractDisease(text); disease != "" && containsAny(text, "disease", "incidence", disease) {
        return agentDecision{Type: "tool", Name: "forecast_disease_incidence", Arguments: map[string]any{"facility": facility, "disease": disease, "horizon_days": days}}, true
    }

    return agentDecision{}, false
}

func lastUserMessage(messages []message) string {
    for i := len(messages)-1; i >= 0; i-- {
        if messages[i].Role == "user" { return messages[i].Content }
    }
    return ""
}

func serviceStatusIntent(text string) bool {
    return containsAny(text, "service status", "system status", "services available", "services are available", "ai services", "what is available", "what's available", "runtime status")
}

func extractHorizon(text string) (int, bool) {
    match := horizonPattern.FindStringSubmatch(text)
    if len(match) != 2 { return 0, false }
    days, err := strconv.Atoi(match[1])
    if err != nil || days < 1 || days > 90 { return 0, false }
    return days, true
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
    for _, disease := range []string{"respiratory", "gastro", "diabetes", "hypertension"} {
        if strings.Contains(text, disease) { return disease }
    }
    return ""
}

func containsAny(value string, needles ...string) bool {
    for _, needle := range needles {
        if strings.Contains(value, needle) { return true }
    }
    return false
}

func (s *server) route(ctx context.Context, messages []message) (agentDecision, error) {
    requestMessages := []message{{Role: "system", Content: routerSystemPrompt}}
    requestMessages = append(requestMessages, compactRoutingHistory(messages)...)
    content, err := s.callAgent(ctx, requestMessages, 128)
    if err != nil { return agentDecision{}, err }
    decision, err := parseAgentDecision(content)
    if err != nil { return agentDecision{}, err }
    return validateDecision(decision)
}

func compactRoutingHistory(messages []message) []message {
    if len(messages) <= maxRoutingHistory { return append([]message(nil), messages...) }
    return append([]message(nil), messages[len(messages)-maxRoutingHistory:]...)
}

func parseAgentDecision(content string) (agentDecision, error) {
    trimmed := strings.TrimSpace(content)
    start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}")
    if start < 0 || end < start { return agentDecision{}, errors.New("agent returned no JSON object") }
    var decision agentDecision
    decoder := json.NewDecoder(strings.NewReader(trimmed[start:end+1]))
    if err := decoder.Decode(&decision); err != nil { return agentDecision{}, fmt.Errorf("invalid agent JSON: %w", err) }
    return decision, nil
}

func validateDecision(decision agentDecision) (agentDecision, error) {
    switch decision.Type {
    case "answer":
        decision.Content = strings.TrimSpace(decision.Content)
        if decision.Content == "" { return agentDecision{}, errors.New("agent answer is empty") }
        if len([]rune(decision.Content)) > 2000 { return agentDecision{}, errors.New("agent answer exceeds output limit") }
        decision.Name, decision.Arguments = "", nil
        return decision, nil
    case "tool":
        if _, ok := allowedTools[decision.Name]; !ok { return agentDecision{}, fmt.Errorf("agent selected unsupported tool: %s", decision.Name) }
        args, err := validateToolArguments(decision.Name, decision.Arguments)
        if err != nil { return agentDecision{}, err }
        decision.Arguments, decision.Content = args, ""
        return decision, nil
    default:
        return agentDecision{}, errors.New("agent returned invalid decision type")
    }
}

func formatToolResult(name string, args map[string]any, result json.RawMessage) (string, error) {
    var payload map[string]any
    if err := json.Unmarshal(result, &payload); err != nil { return "", fmt.Errorf("invalid tool result: %w", err) }

    switch name {
    case "forecast_patient_volume", "forecast_bed_occupancy", "forecast_disease_incidence":
        expected, okExpected := number(payload["expected"])
        p10, okP10 := number(payload["p10"])
        p90, okP90 := number(payload["p90"])
        if !okExpected || !okP10 || !okP90 { return compactToolFallback(name, payload), nil }
        horizon, _ := numberAsInt(args["horizon_days"])
        if name == "forecast_disease_incidence" {
            return fmt.Sprintf("%d-day %s incidence forecast for JNF: expected %s, with a P10-P90 range of %s-%s. Synthetic operational prototype data; not a clinical diagnosis.", horizon, stringArg(args, "disease"), formatNumber(expected), formatNumber(p10), formatNumber(p90)), nil
        }
        metric := "patient arrivals"
        if name == "forecast_bed_occupancy" { metric = "bed occupancy" }
        return fmt.Sprintf("%d-day %s forecast for %s at JNF: expected %s, with a P10-P90 range of %s-%s.", horizon, metric, stringArg(args, "department"), formatNumber(expected), formatNumber(p10), formatNumber(p90)), nil

    case "get_radiology_result":
        findings, _ := payload["findings"].(string)
        findings = strings.TrimSpace(findings)
        if findings == "" { return compactToolFallback(name, payload), nil }
        resultID, _ := payload["result_id"].(string)
        if resultID == "" { resultID = stringArg(args, "result_id") }
        return fmt.Sprintf("Radiology result %s: %s Clinician review is required before any clinical use.", resultID, findings), nil

    case "get_service_status":
        services, _ := payload["services"].(map[string]any)
        if len(services) == 0 { return "The local NDHIS AI gateway is responding, but no specialist service states were returned.", nil }
        keys := make([]string, 0, len(services))
        for key := range services { keys = append(keys, key) }
        sort.Strings(keys)
        states := make([]string, 0, len(keys))
        for _, key := range keys { states = append(states, fmt.Sprintf("%s: %v", key, services[key])) }
        return "Local AI service status — " + strings.Join(states, "; ") + ".", nil

    default:
        return "", fmt.Errorf("unsupported tool result: %s", name)
    }
}

func compactToolFallback(name string, payload map[string]any) string {
    encoded, _ := json.Marshal(payload)
    text := string(encoded)
    if len(text) > 1200 { text = text[:1200] + "…" }
    return fmt.Sprintf("%s completed successfully. Result: %s", name, text)
}

func stringArg(args map[string]any, key string) string {
    value, _ := args[key].(string)
    return value
}

func number(value any) (float64, bool) {
    switch typed := value.(type) {
    case float64:
        return typed, true
    case int:
        return float64(typed), true
    case json.Number:
        parsed, err := typed.Float64()
        return parsed, err == nil
    default:
        return 0, false
    }
}

func formatNumber(value float64) string {
    if value == float64(int64(value)) { return strconv.FormatInt(int64(value), 10) }
    return strconv.FormatFloat(value, 'f', 1, 64)
}

func (s *server) callAgent(ctx context.Context, messages []message, maxTokens int) (string, error) {
    body, err := json.Marshal(agentRequest{
        Model: s.cfg.AgentModel,
        Messages: messages,
        Temperature: 0,
        MaxTokens: maxTokens,
        ResponseFormat: map[string]string{"type": "json_object"},
        CachePrompt: true,
    })
    if err != nil { return "", err }
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AgentURL, bytes.NewReader(body))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/json")
    resp, err := s.client.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
        return "", fmt.Errorf("agent status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
    }
    var output agentResponse
    decoder := json.NewDecoder(io.LimitReader(resp.Body, maxAgentResponse))
    if err := decoder.Decode(&output); err != nil { return "", fmt.Errorf("invalid agent response: %w", err) }
    if len(output.Choices) == 0 { return "", errors.New("agent returned no choices") }
    content := strings.TrimSpace(output.Choices[0].Message.Content)
    if content == "" { return "", errors.New("agent returned empty content") }
    return content, nil
}
