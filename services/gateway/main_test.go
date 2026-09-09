package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)
func TestRateLimitPerUser(t *testing.T){s:=&server{cfg:config{RequestsPerMin:2},rates:map[string]rateState{}};if !s.allow("doctor-a"){t.Fatal("first request should pass")};if !s.allow("doctor-a"){t.Fatal("second request should pass")};if s.allow("doctor-a"){t.Fatal("third request should be rate limited")};if !s.allow("doctor-b"){t.Fatal("rate limit should be per user")}}
func testServer()*server{return &server{cfg:config{DemoKey:"key",RequestsPerMin:10,MaxBodyBytes:1024},rates:map[string]rateState{},semaphore:make(chan struct{},1)}}
func TestGuardRequiresIdentity(t *testing.T){handler:=testServer().guard(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)});req:=httptest.NewRequest(http.MethodGet,"/",nil);req.Header.Set("X-NDHIS-Demo-Key","key");res:=httptest.NewRecorder();handler(res,req);if res.Code!=http.StatusBadRequest{t.Fatalf("expected 400, got %d",res.Code)}}
func TestGuardAcceptsAuthorizedDoctor(t *testing.T){handler:=testServer().guard(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)});req:=httptest.NewRequest(http.MethodGet,"/",nil);req.Header.Set("X-NDHIS-Demo-Key","key");req.Header.Set("X-NDHIS-User","doctor-a");req.Header.Set("X-NDHIS-Role","doctor");res:=httptest.NewRecorder();handler(res,req);if res.Code!=http.StatusNoContent{t.Fatalf("expected 204, got %d",res.Code)}}
func TestGuardRejectsNonDoctorRole(t *testing.T){handler:=testServer().guard(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)});req:=httptest.NewRequest(http.MethodGet,"/",nil);req.Header.Set("X-NDHIS-Demo-Key","key");req.Header.Set("X-NDHIS-User","admin-a");req.Header.Set("X-NDHIS-Role","admin");res:=httptest.NewRecorder();handler(res,req);if res.Code!=http.StatusForbidden{t.Fatalf("expected 403, got %d",res.Code)}}
func TestValidateMessages(t *testing.T){if err:=validateMessages([]message{{Role:"user",Content:"forecast A&E"}});err!=nil{t.Fatal(err)};if err:=validateMessages([]message{{Role:"system",Content:"nope"}});err==nil{t.Fatal("system role should be rejected")};if err:=validateMessages(nil);err==nil{t.Fatal("empty messages should be rejected")}}
func TestValidateToolArguments(t *testing.T){args,err:=validateToolArguments("forecast_patient_volume",map[string]any{"facility":"JNF","department":"A&E","horizon_days":float64(30),"ignored":"drop me"});if err!=nil{t.Fatal(err)};if len(args)!=3{t.Fatalf("expected sanitized args, got %#v",args)};if _,err:=validateToolArguments("forecast_patient_volume",map[string]any{"facility":"JNF","department":"A&E","horizon_days":float64(365)});err==nil{t.Fatal("oversized horizon should be rejected")}}
func TestParseAgentDecisionToleratesWrapperText(t *testing.T){decision,err:=parseAgentDecision("result: {\"type\":\"answer\",\"content\":\"ready\"}");if err!=nil{t.Fatal(err)};if decision.Type!="answer"||decision.Content!="ready"{t.Fatalf("unexpected decision: %#v",decision)}}
func TestRecentAuditRedactsIdentity(t *testing.T){dir:=t.TempDir();path:=filepath.Join(dir,"audit.jsonl");event:=auditEvent{Timestamp:"2026-09-09T12:00:00Z",RequestID:"req-1",User:"doctor-secret",Role:"doctor",Route:"/api/chat",Tool:"get_service_status",Model:"Qwen",Status:200,LatencyMS:12};payload,_:=json.Marshal(event);if err:=os.WriteFile(path,append(payload,'\n'),0600);err!=nil{t.Fatal(err)};events,err:=readRecentAudit(path,10);if err!=nil{t.Fatal(err)};if len(events)!=1{t.Fatalf("expected one event, got %d",len(events))};encoded,_:=json.Marshal(events[0]);if string(encoded)==""||contains(string(encoded),"doctor-secret"){t.Fatalf("identity leaked: %s",encoded)}}
func contains(value,needle string)bool{for i:=0;i+len(needle)<=len(value);i++{if value[i:i+len(needle)]==needle{return true}};return false}
