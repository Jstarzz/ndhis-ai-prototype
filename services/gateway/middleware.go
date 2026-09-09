package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *server) securityHeaders(next http.Handler) http.Handler { return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ w.Header().Set("Cache-Control","no-store"); w.Header().Set("Pragma","no-cache"); w.Header().Set("X-Content-Type-Options","nosniff"); w.Header().Set("X-Frame-Options","DENY"); w.Header().Set("Referrer-Policy","no-referrer"); next.ServeHTTP(w,r) }) }
func (s *server) cors(next http.Handler) http.Handler { return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ w.Header().Set("Access-Control-Allow-Origin","*"); w.Header().Set("Access-Control-Allow-Headers","Content-Type, X-NDHIS-Demo-Key, X-NDHIS-User, X-NDHIS-Role"); w.Header().Set("Access-Control-Allow-Methods","GET, POST, OPTIONS"); w.Header().Set("Access-Control-Expose-Headers","X-NDHIS-Request-ID"); if r.Method==http.MethodOptions { w.WriteHeader(http.StatusNoContent); return }; next.ServeHTTP(w,r) }) }
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc { return func(w http.ResponseWriter,r *http.Request){
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-NDHIS-Demo-Key")),[]byte(s.cfg.DemoKey))!=1 { writeJSON(w,http.StatusUnauthorized,map[string]string{"error":"unauthorized"}); return }
	user,role:=strings.TrimSpace(r.Header.Get("X-NDHIS-User")),strings.TrimSpace(r.Header.Get("X-NDHIS-Role")); if user==""||role=="" { writeJSON(w,http.StatusBadRequest,map[string]string{"error":"identity headers required"}); return }; if len(user)>128||len(role)>64 { writeJSON(w,http.StatusBadRequest,map[string]string{"error":"identity headers too long"}); return }; if !strings.EqualFold(role,"doctor") { writeJSON(w,http.StatusForbidden,map[string]string{"error":"doctor role required for this prototype"}); return }; if !s.allow(user) { w.Header().Set("Retry-After","60"); writeJSON(w,http.StatusTooManyRequests,map[string]string{"error":"rate limit exceeded"}); return }
	select { case s.semaphore<-struct{}{}: defer func(){<-s.semaphore}(); default: w.Header().Set("Retry-After","2"); writeJSON(w,http.StatusServiceUnavailable,map[string]string{"error":"AI gateway at concurrency limit"}); return }
	if r.Body!=nil { r.Body=http.MaxBytesReader(w,r.Body,s.cfg.MaxBodyBytes) }; next(w,r)
} }
func (s *server) allow(user string) bool { now:=time.Now(); s.rateMu.Lock(); defer s.rateMu.Unlock(); state:=s.rates[user]; if state.Window.IsZero()||now.Sub(state.Window)>=time.Minute { s.rates[user]=rateState{Window:now,Count:1}; return true }; if state.Count>=s.cfg.RequestsPerMin { return false }; state.Count++; s.rates[user]=state; return true }
func (s *server) health(w http.ResponseWriter,r *http.Request){ ctx,cancel:=context.WithTimeout(r.Context(),5*time.Second); defer cancel(); writeJSON(w,http.StatusOK,map[string]any{"status":"ok","services":s.serviceStatus(ctx)}) }
func (s *server) systemInfo(w http.ResponseWriter,r *http.Request){ ctx,cancel:=context.WithTimeout(r.Context(),5*time.Second); defer cancel(); writeJSON(w,http.StatusOK,map[string]any{"processing":"local","profile":s.cfg.RuntimeProfile,"services":s.serviceStatus(ctx),"models":map[string]string{"agent":s.cfg.AgentModelLabel,"asr":s.cfg.ASRModel,"translation":s.cfg.TranslationModel,"forecasting":s.cfg.ForecastModel,"radiology":s.cfg.RadiologyModel},"limits":map[string]any{"requests_per_minute_per_user":s.cfg.RequestsPerMin,"max_concurrent_requests":s.cfg.MaxConcurrent,"max_body_bytes":s.cfg.MaxBodyBytes}}) }
func (s *server) serviceStatus(ctx context.Context) map[string]string { checks:=map[string]string{}; checks["agent"]=s.check(ctx,strings.TrimSuffix(s.cfg.AgentURL,"/v1/chat/completions")+"/v1/models"); checks["forecasting"]=s.check(ctx,s.cfg.ForecastURL+"/health"); checks["radiology"]=s.check(ctx,s.cfg.RadiologyURL+"/health"); translationHTTP:=strings.Replace(strings.TrimSuffix(s.cfg.TranslationURL,"/ws/translate"),"ws://","http://",1); translationHTTP=strings.Replace(translationHTTP,"wss://","https://",1); checks["translation"]=s.check(ctx,translationHTTP+"/health"); return checks }
func (s *server) check(ctx context.Context,endpoint string) string { req,err:=http.NewRequestWithContext(ctx,http.MethodGet,endpoint,nil); if err!=nil{return "error"}; resp,err:=s.client.Do(req); if err!=nil{return "offline"}; defer resp.Body.Close(); if resp.StatusCode<200||resp.StatusCode>=300{return "error"}; var health struct{Status string `json:"status"`}; if err:=json.NewDecoder(io.LimitReader(resp.Body,4096)).Decode(&health);err==nil{switch strings.ToLower(strings.TrimSpace(health.Status)){case "degraded":return "degraded";case "error","failed":return "error";case "offline":return "offline"}}; return "ready" }
