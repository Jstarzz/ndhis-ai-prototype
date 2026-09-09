package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRateLimitPerUser(t *testing.T) {
	s := &server{cfg: config{RequestsPerMin: 2}, rates: map[string]rateState{}}
	if !s.allow("doctor-a") { t.Fatal("first request should pass") }
	if !s.allow("doctor-a") { t.Fatal("second request should pass") }
	if s.allow("doctor-a") { t.Fatal("third request should be rate limited") }
	if !s.allow("doctor-b") { t.Fatal("rate limit should be per user") }
}

func testServer() *server {
	return &server{cfg: config{DemoKey: "key", RequestsPerMin: 10, MaxBodyBytes: 1024, AgentMaxTokens: 128}, rates: map[string]rateState{}, semaphore: make(chan struct{}, 1)}
}

func TestGuardRequiresIdentity(t *testing.T) {
	handler := testServer().guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-NDHIS-Demo-Key", "key")
	res := httptest.NewRecorder()
	handler(res, req)
	if res.Code != http.StatusBadRequest { t.Fatalf("expected 400, got %d", res.Code) }
}

func TestGuardAcceptsAuthorizedDoctor(t *testing.T) {
	handler := testServer().guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-NDHIS-Demo-Key", "key")
	req.Header.Set("X-NDHIS-User", "doctor-a")
	req.Header.Set("X-NDHIS-Role", "doctor")
	res := httptest.NewRecorder()
	handler(res, req)
	if res.Code != http.StatusNoContent { t.Fatalf("expected 204, got %d", res.Code) }
}

func TestGuardRejectsNonDoctorRole(t *testing.T) {
	handler := testServer().guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-NDHIS-Demo-Key", "key")
	req.Header.Set("X-NDHIS-User", "admin-a")
	req.Header.Set("X-NDHIS-Role", "admin")
	res := httptest.NewRecorder()
	handler(res, req)
	if res.Code != http.StatusForbidden { t.Fatalf("expected 403, got %d", res.Code) }
}

func TestValidateMessages(t *testing.T) {
	if err := validateMessages([]message{{Role: "user", Content: "forecast A&E"}}); err != nil { t.Fatal(err) }
	if err := validateMessages([]message{{Role: "system", Content: "nope"}}); err == nil { t.Fatal("system role should be rejected") }
	if err := validateMessages(nil); err == nil { t.Fatal("empty messages should be rejected") }
}

func TestValidateToolArgumentsLegacyDays(t *testing.T) {
	args, err := validateToolArguments("forecast_patient_volume", map[string]any{"facility": "JNF", "department": "A&E", "horizon_days": float64(30), "ignored": "drop me"})
	if err != nil { t.Fatal(err) }
	if args["facility"] != "JNF" || args["department"] != "A&E" || args["horizon"] != 30 || args["horizon_unit"] != "days" { t.Fatalf("unexpected sanitized args: %#v", args) }
	if _, ok := args["ignored"]; ok { t.Fatalf("unexpected unknown arg: %#v", args) }
}

func TestValidateToolArgumentsMultiscale(t *testing.T) {
	args, err := validateToolArguments("forecast_bed_occupancy", map[string]any{"facility":"jnf","department":"medical ward","horizon":float64(2),"horizon_unit":"hours","resolution":"5min"})
	if err != nil { t.Fatal(err) }
	if args["facility"] != "JNF" || args["department"] != "Medical Ward" || args["resolution"] != "5min" { t.Fatalf("unexpected args: %#v", args) }
	if _, err := validateToolArguments("forecast_patient_volume", map[string]any{"facility":"JNF","department":"ICU","horizon":1,"horizon_unit":"days"}); err == nil { t.Fatal("unsupported department should be rejected") }
	if _, err := validateToolArguments("forecast_patient_volume", map[string]any{"facility":"JNF","department":"A&E","horizon":3,"horizon_unit":"years"}); err == nil { t.Fatal("horizon over two years should be rejected") }
}

func TestParseAgentDecisionToleratesWrapperText(t *testing.T) {
	decision, err := parseAgentDecision("result: {\"type\":\"answer\",\"content\":\"ready\"}")
	if err != nil { t.Fatal(err) }
	if decision.Type != "answer" || decision.Content != "ready" { t.Fatalf("unexpected decision: %#v", decision) }
}

func TestDeterministicRouteServiceStatus(t *testing.T) {
	decision, ok := deterministicRoute([]message{{Role: "user", Content: "What AI services are currently available?"}})
	if !ok { t.Fatal("expected deterministic route") }
	if decision.Name != "get_service_status" { t.Fatalf("unexpected tool: %s", decision.Name) }
}

func TestDeterministicRoutePatientForecast(t *testing.T) {
	decision, ok := deterministicRoute([]message{{Role: "user", Content: "Forecast A&E patient arrivals for the next 30 days."}})
	if !ok { t.Fatal("expected deterministic route") }
	if decision.Name != "forecast_patient_volume" { t.Fatalf("unexpected tool: %s", decision.Name) }
	if decision.Arguments["department"] != "A&E" || decision.Arguments["horizon"] != float64(30) || decision.Arguments["horizon_unit"] != "days" { t.Fatalf("unexpected args: %#v", decision.Arguments) }
}

func TestDeterministicRouteHourlyForecastWithResolution(t *testing.T) {
	decision, ok := deterministicRoute([]message{{Role: "user", Content: "Forecast A&E patient arrivals for the next 2 hours every 5 minutes."}})
	if !ok { t.Fatal("expected deterministic route") }
	if decision.Name != "forecast_patient_volume" { t.Fatalf("unexpected tool: %s", decision.Name) }
	if decision.Arguments["horizon"] != float64(2) || decision.Arguments["horizon_unit"] != "hours" || decision.Arguments["resolution"] != "5min" { t.Fatalf("unexpected args: %#v", decision.Arguments) }
}

func TestDeterministicRouteDiseaseForecast(t *testing.T) {
	decision, ok := deterministicRoute([]message{{Role: "user", Content: "Forecast respiratory disease incidence at JNF for 14 days."}})
	if !ok { t.Fatal("expected deterministic route") }
	if decision.Name != "forecast_disease_incidence" { t.Fatalf("unexpected tool: %s", decision.Name) }
	if decision.Arguments["disease"] != "respiratory" || decision.Arguments["horizon"] != float64(14) { t.Fatalf("unexpected args: %#v", decision.Arguments) }
}

func TestForecastSlotFillingAcrossTurns(t *testing.T) {
	messages := []message{
		{Role:"user", Content:"Forecasts for the next 2 hours?"},
		{Role:"assistant", Content:"Which metric and department?"},
		{Role:"user", Content:"A&E"},
	}
	decision, ok, intent := deterministicRouteDetailed(messages)
	if !ok || decision.Type != "answer" || intent != "forecast_clarification" { t.Fatalf("expected deterministic clarification, got %#v %v %s", decision, ok, intent) }
	messages = append(messages, message{Role:"assistant", Content:decision.Content}, message{Role:"user", Content:"patient arrivals every 5 minutes"})
	decision, ok, intent = deterministicRouteDetailed(messages)
	if !ok || decision.Name != "forecast_patient_volume" || intent != "patient_volume_forecast" { t.Fatalf("expected completed slot route, got %#v %v %s", decision, ok, intent) }
	if decision.Arguments["department"] != "A&E" || decision.Arguments["horizon_unit"] != "hours" || decision.Arguments["resolution"] != "5min" { t.Fatalf("unexpected carried context: %#v", decision.Arguments) }
}

func TestForecastCapabilitiesAreDeterministic(t *testing.T) {
	decision, ok, intent := deterministicRouteDetailed([]message{{Role:"user", Content:"Can you tell me all of the facilities and departments?"}})
	if !ok || decision.Type != "answer" || intent != "forecast_capabilities" { t.Fatalf("unexpected result: %#v %v %s", decision, ok, intent) }
	if !strings.Contains(decision.Content, "A&E") || !strings.Contains(decision.Content, "2020") { t.Fatalf("capabilities missing domain: %s", decision.Content) }
}

func TestCompactRoutingHistory(t *testing.T) {
	messages := make([]message, 0, 10)
	for i := 0; i < 10; i++ { messages = append(messages, message{Role: "user", Content: string(rune('a' + i))}) }
	compact := compactRoutingHistory(messages)
	if len(compact) != maxRoutingHistory { t.Fatalf("expected %d messages, got %d", maxRoutingHistory, len(compact)) }
	if compact[0].Content != "g" || compact[len(compact)-1].Content != "j" { t.Fatalf("unexpected compact history: %#v", compact) }
}

func TestFormatForecastToolResult(t *testing.T) {
	payload := json.RawMessage(`{"expected":42,"p10":35,"p90":50,"resolution":"5min","horizon":{"value":2,"unit":"hours"},"intensity":{"expected_per_hour":21},"backtest":{"available":false}}`)
	answer, err := formatToolResult("forecast_patient_volume", map[string]any{"department":"A&E","horizon":2,"horizon_unit":"hours","resolution":"5min"}, payload)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(answer, "2 hours") || !strings.Contains(answer, "42") || !strings.Contains(answer, "35-50") || !strings.Contains(answer, "5min") { t.Fatalf("unexpected answer: %s", answer) }
}

func TestRecentAuditRedactsIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	event := auditEvent{Timestamp: "2026-09-09T12:00:00Z", RequestID: "req-1", User: "doctor-secret", Role: "doctor", Route: "/api/chat", Tool: "get_service_status", Model: "Qwen", Status: 200, LatencyMS: 12}
	payload, _ := json.Marshal(event)
	if err := os.WriteFile(path, append(payload, '\n'), 0600); err != nil { t.Fatal(err) }
	events, err := readRecentAudit(path, 10)
	if err != nil { t.Fatal(err) }
	if len(events) != 1 { t.Fatalf("expected one event, got %d", len(events)) }
	encoded, _ := json.Marshal(events[0])
	if strings.Contains(string(encoded), "doctor-secret") { t.Fatalf("identity leaked: %s", encoded) }
}
