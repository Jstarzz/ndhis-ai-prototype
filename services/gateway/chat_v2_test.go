package main

import (
	"strings"
	"testing"
)

func TestV2NormalizesColloquialTimeAndDepartment(t *testing.T) {
	text := normalizeConversationalTextV2("Gimme forecasts for A and E patients for the next minute and a half")
	if !strings.Contains(text, "a&e patient arrivals") || !strings.Contains(text, "1.5 minutes") {
		t.Fatalf("unexpected normalization: %s", text)
	}
}

func TestV2DeterministicColloquialForecast(t *testing.T) {
	messages := normalizeMessagesV2([]message{{Role: "user", Content: "Gimme forecasts for A and E patients for the next minute and a half"}})
	decision, ok, intent := directRouteV2(messages)
	if !ok || decision.Name != "forecast_patient_volume" || intent != "patient_volume_forecast" {
		t.Fatalf("unexpected route: %#v %v %s", decision, ok, intent)
	}
	if decision.Arguments["department"] != "A&E" || decision.Arguments["horizon"] != float64(1.5) || decision.Arguments["horizon_unit"] != "minutes" {
		t.Fatalf("unexpected args: %#v", decision.Arguments)
	}
}

func TestV2ColloquialSlotFill(t *testing.T) {
	messages := []message{{Role: "user", Content: "Forecast A and E patients"}}
	normalized := normalizeMessagesV2(messages)
	decision, ok, intent := directRouteV2(normalized)
	if !ok || decision.Type != "answer" || intent != "forecast_clarification" {
		t.Fatalf("expected clarification: %#v %v %s", decision, ok, intent)
	}
	messages = append(messages, message{Role: "assistant", Content: decision.Content}, message{Role: "user", Content: "minute and a half"})
	normalized = normalizeMessagesV2(messages)
	decision, ok, intent = directRouteV2(normalized)
	if !ok || decision.Name != "forecast_patient_volume" || intent != "patient_volume_forecast" {
		t.Fatalf("expected completed route: %#v %v %s", decision, ok, intent)
	}
	if decision.Arguments["horizon"] != float64(1.5) || decision.Arguments["horizon_unit"] != "minutes" {
		t.Fatalf("unexpected carried horizon: %#v", decision.Arguments)
	}
}

func TestV2HalfHourAndSpokenResolution(t *testing.T) {
	text := normalizeConversationalTextV2("Forecast A&E patients for half an hour every five minutes")
	if !strings.Contains(text, "0.5 hours") || !strings.Contains(text, "every 5 minutes") {
		t.Fatalf("unexpected normalization: %s", text)
	}
	decision, ok, _ := directRouteV2([]message{{Role: "user", Content: text}})
	if !ok || decision.Name != "forecast_patient_volume" {
		t.Fatalf("unexpected route: %#v", decision)
	}
	if decision.Arguments["resolution"] != "5min" {
		t.Fatalf("unexpected resolution: %#v", decision.Arguments)
	}
}

func TestV2DirectRadiologyResultLookup(t *testing.T) {
	decision, ok, intent := directRouteV2([]message{{Role: "user", Content: "Show me radiology result 0dc3444c774c2fec"}})
	if !ok || decision.Name != "get_radiology_result" || intent != "radiology_result" {
		t.Fatalf("unexpected route: %#v %v %s", decision, ok, intent)
	}
	if decision.Arguments["result_id"] != "0dc3444c774c2fec" {
		t.Fatalf("unexpected result id: %#v", decision.Arguments)
	}
}

func TestV2AssistantCapabilitiesAreLocal(t *testing.T) {
	decision, ok, intent := directRouteV2([]message{{Role: "user", Content: "What does this do?"}})
	if !ok || decision.Type != "answer" || intent != "assistant_capabilities" {
		t.Fatalf("unexpected route: %#v %v %s", decision, ok, intent)
	}
	if !strings.Contains(decision.Content, "translation") || !strings.Contains(decision.Content, "radiology") {
		t.Fatalf("capability answer incomplete: %s", decision.Content)
	}
}

func TestV2NuancedOperationalRequestUsesRouter(t *testing.T) {
	messages := normalizeMessagesV2([]message{{Role: "user", Content: "Emergency seems unusually busy right now. What do the next three hours look like?"}})
	if !operationalCandidateV2(messages) {
		t.Fatal("expected nuanced operational request to use constrained router")
	}
}

func TestV2GeneralQuestionUsesAssistant(t *testing.T) {
	messages := normalizeMessagesV2([]message{{Role: "user", Content: "Why can local inference be useful in a hospital?"}})
	if operationalCandidateV2(messages) {
		t.Fatal("general explanation should not be forced through tool router")
	}
}

func TestV2RoutingContextIsStructuredAndBounded(t *testing.T) {
	messages := normalizeMessagesV2([]message{
		{Role: "user", Content: "Forecast A&E patient arrivals for the next two hours"},
		{Role: "assistant", Content: strings.Repeat("x", 500)},
		{Role: "user", Content: "What about every five minutes?"},
	})
	context := routingContextV2(messages)
	if !strings.Contains(context, "forecast=active") || !strings.Contains(context, "department=A&E") || !strings.Contains(context, "resolution=5min") {
		t.Fatalf("missing state: %s", context)
	}
	if len(context) > 1400 {
		t.Fatalf("routing context too large: %d", len(context))
	}
}

func BenchmarkV2DeterministicColloquialForecast(b *testing.B) {
	messages := []message{{Role: "user", Content: "Gimme forecasts for A and E patients for the next minute and a half"}}
	for index := 0; index < b.N; index++ {
		normalized := normalizeMessagesV2(messages)
		_, _, _ = directRouteV2(normalized)
	}
}

func BenchmarkV2NormalizeSpokenDuration(b *testing.B) {
	for index := 0; index < b.N; index++ {
		_ = normalizeConversationalTextV2("Forecast A and E patients for two hours and a half every five minutes")
	}
}
