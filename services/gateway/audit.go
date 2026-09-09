package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type auditEvent struct { Timestamp string `json:"timestamp"`; RequestID string `json:"request_id"`; User string `json:"user"`; Role string `json:"role"`; Route string `json:"route"`; Tool string `json:"tool,omitempty"`; Model string `json:"model,omitempty"`; Status int `json:"status"`; LatencyMS int64 `json:"latency_ms"` }
type publicAuditEvent struct { Timestamp string `json:"timestamp"`; RequestID string `json:"request_id"`; Route string `json:"route"`; Tool string `json:"tool,omitempty"`; Model string `json:"model,omitempty"`; Status int `json:"status"`; LatencyMS int64 `json:"latency_ms"` }
func (s *server) recentAudit(w http.ResponseWriter,r *http.Request){limit:=12;if raw:=r.URL.Query().Get("limit");raw!=""{parsed,err:=strconv.Atoi(raw);if err!=nil||parsed<1||parsed>maxAuditEvents{writeJSON(w,http.StatusBadRequest,map[string]string{"error":"limit must be between 1 and 100"});return};limit=parsed};events,err:=readRecentAudit(s.cfg.AuditPath,limit);if err!=nil{if errors.Is(err,os.ErrNotExist){writeJSON(w,http.StatusOK,map[string]any{"events":[]publicAuditEvent{}});return};writeJSON(w,http.StatusInternalServerError,map[string]string{"error":"audit log unavailable"});return};writeJSON(w,http.StatusOK,map[string]any{"events":events})}
func readRecentAudit(path string,limit int)([]publicAuditEvent,error){file,err:=os.Open(path);if err!=nil{return nil,err};defer file.Close();stat,err:=file.Stat();if err!=nil{return nil,err};start:=stat.Size()-maxAuditTailBytes;if start<0{start=0};if _,err:=file.Seek(start,io.SeekStart);err!=nil{return nil,err};scanner:=bufio.NewScanner(file);scanner.Buffer(make([]byte,64*1024),512*1024);if start>0&&scanner.Scan(){};all:=make([]publicAuditEvent,0,limit);for scanner.Scan(){var event auditEvent;if err:=json.Unmarshal(scanner.Bytes(),&event);err!=nil{continue};all=append(all,publicAuditEvent{Timestamp:event.Timestamp,RequestID:event.RequestID,Route:event.Route,Tool:event.Tool,Model:event.Model,Status:event.Status,LatencyMS:event.LatencyMS});if len(all)>limit{all=all[len(all)-limit:]}};if err:=scanner.Err();err!=nil{return nil,err};for left,right:=0,len(all)-1;left<right;left,right=left+1,right-1{all[left],all[right]=all[right],all[left]};return all,nil}
func (s *server) audit(r *http.Request,requestID,route,tool string,status int,started time.Time){event:=auditEvent{Timestamp:time.Now().UTC().Format(time.RFC3339Nano),RequestID:requestID,User:r.Header.Get("X-NDHIS-User"),Role:r.Header.Get("X-NDHIS-Role"),Route:route,Tool:tool,Model:s.cfg.AgentModelLabel,Status:status,LatencyMS:time.Since(started).Milliseconds()};payload,err:=json.Marshal(event);if err!=nil{return};s.auditMu.Lock();defer s.auditMu.Unlock();file,err:=os.OpenFile(s.cfg.AuditPath,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{log.Printf("audit error: %v",err);return};defer file.Close();_,_=file.Write(append(payload,'\n'))}
func mustJSON(value any) string {payload,_:=json.Marshal(value);return string(payload)}
func writeJSON(w http.ResponseWriter,status int,value any){w.Header().Set("Content-Type","application/json; charset=utf-8");w.WriteHeader(status);_=json.NewEncoder(w).Encode(value)}
