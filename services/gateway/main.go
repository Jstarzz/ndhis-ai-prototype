package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxChatMessages      = 24
	maxMessageCharacters = 8000
	maxAgentResponse     = 2 << 20
	maxToolResponse      = 4 << 20
	maxAuditTailBytes    = 1 << 20
	maxAuditEvents       = 100
)

type config struct {
	RuntimeProfile   string
	DemoKey          string
	AgentURL         string
	AgentModel       string
	AgentModelLabel  string
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

type rateState struct { Window time.Time; Count int }
type message struct { Role string `json:"role"`; Content string `json:"content"` }
type chatRequest struct { Messages []message `json:"messages"` }
type agentRequest struct { Model string `json:"model"`; Messages []message `json:"messages"`; Temperature float64 `json:"temperature"`; MaxTokens int `json:"max_tokens"`; ResponseFormat map[string]string `json:"response_format"` }
type agentResponse struct { Choices []struct { Message struct { Content string `json:"content"` } `json:"message"` } `json:"choices"` }
type agentDecision struct { Type string `json:"type"`; Content string `json:"content,omitempty"`; Name string `json:"name,omitempty"`; Arguments map[string]any `json:"arguments,omitempty"` }
type chatResponse struct { Answer string `json:"answer"`; Tool string `json:"tool,omitempty"`; Arguments map[string]any `json:"arguments,omitempty"`; LatencyMS int64 `json:"latency_ms"`; RequestID string `json:"request_id"` }

var toolManifest = []map[string]any{
	{"name":"forecast_patient_volume","description":"Forecast patient arrivals for a facility and department","arguments":map[string]any{"facility":"string","department":"string","horizon_days":"integer 1-90"}},
	{"name":"forecast_bed_occupancy","description":"Forecast bed occupancy for a facility and department","arguments":map[string]any{"facility":"string","department":"string","horizon_days":"integer 1-90"}},
	{"name":"forecast_disease_incidence","description":"Forecast disease incidence for a named disease category","arguments":map[string]any{"facility":"string","disease":"string","horizon_days":"integer 1-90"}},
	{"name":"get_radiology_result","description":"Retrieve an already analyzed radiology study by result id","arguments":map[string]any{"result_id":"string"}},
	{"name":"get_service_status","description":"Get local AI service status","arguments":map[string]any{}},
}
var allowedTools = map[string]struct{}{"forecast_patient_volume":{},"forecast_bed_occupancy":{},"forecast_disease_incidence":{},"get_radiology_result":{},"get_service_status":{}}

func main() {
	cfg := config{RuntimeProfile:mustEnv("RUNTIME_PROFILE"),DemoKey:mustEnv("DEMO_API_KEY"),AgentURL:mustEnv("AGENT_URL"),AgentModel:mustEnv("AGENT_MODEL_NAME"),AgentModelLabel:optionalEnv("AGENT_MODEL_LABEL",mustEnv("AGENT_MODEL_NAME")),TranslationURL:mustEnv("TRANSLATION_URL"),ForecastURL:mustEnv("FORECAST_URL"),RadiologyURL:mustEnv("RADIOLOGY_URL"),AuditPath:mustEnv("AUDIT_PATH"),RequestsPerMin:mustIntEnv("REQUESTS_PER_MINUTE"),MaxConcurrent:mustIntEnv("MAX_CONCURRENT_REQUESTS"),MaxBodyBytes:int64(mustIntEnv("MAX_BODY_MB"))*1024*1024,ASRModel:mustEnv("ASR_MODEL_NAME"),TranslationModel:mustEnv("TRANSLATION_MODEL_NAME"),ForecastModel:mustEnv("FORECAST_MODEL_NAME"),RadiologyModel:mustEnv("RADIOLOGY_MODEL_NAME")}
	s := &server{cfg:cfg,client:&http.Client{Timeout:90*time.Second,Transport:&http.Transport{MaxIdleConns:32,MaxIdleConnsPerHost:8,IdleConnTimeout:90*time.Second}},rates:map[string]rateState{},semaphore:make(chan struct{},cfg.MaxConcurrent)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health",s.health); mux.HandleFunc("GET /api/system",s.guard(s.systemInfo)); mux.HandleFunc("GET /api/audit/recent",s.guard(s.recentAudit)); mux.HandleFunc("POST /api/chat",s.guard(s.chat)); mux.HandleFunc("POST /api/forecast",s.guard(s.forecastProxy)); mux.HandleFunc("POST /api/radiology",s.guard(s.radiologyProxy)); mux.HandleFunc("GET /api/radiology/{id}",s.guard(s.radiologyResultProxy))
	fmt.Println("gateway listening on :8080")
	if err := http.ListenAndServe(":8080",s.securityHeaders(s.cors(mux))); err != nil { panic(err) }
}
func mustEnv(key string) string { value:=strings.TrimSpace(os.Getenv(key)); if value=="" { panic("missing environment variable: "+key) }; return value }
func optionalEnv(key,fallback string) string { value:=strings.TrimSpace(os.Getenv(key)); if value=="" { return fallback }; return value }
func mustIntEnv(key string) int { value:=mustEnv(key); parsed,err:=strconv.Atoi(value); if err!=nil||parsed<=0 { panic("invalid positive integer environment variable: "+key) }; return parsed }
