package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitPerUser(t *testing.T) {
	s := &server{cfg: config{RequestsPerMin: 2}, rates: map[string]rateState{}}
	if !s.allow("doctor-a") {
		t.Fatal("first request should pass")
	}
	if !s.allow("doctor-a") {
		t.Fatal("second request should pass")
	}
	if s.allow("doctor-a") {
		t.Fatal("third request should be rate limited")
	}
	if !s.allow("doctor-b") {
		t.Fatal("rate limit should be per user")
	}
}

func TestGuardRequiresIdentity(t *testing.T) {
	s := &server{
		cfg:       config{DemoKey: "key", RequestsPerMin: 2, MaxBodyBytes: 1024},
		rates:     map[string]rateState{},
		semaphore: make(chan struct{}, 1),
	}
	handler := s.guard(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-NDHIS-Demo-Key", "key")
	res := httptest.NewRecorder()
	handler(res, req)
	if res.Code != 400 {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestGuardAcceptsAuthorizedDoctor(t *testing.T) {
	s := &server{
		cfg:       config{DemoKey: "key", RequestsPerMin: 2, MaxBodyBytes: 1024},
		rates:     map[string]rateState{},
		semaphore: make(chan struct{}, 1),
	}
	handler := s.guard(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-NDHIS-Demo-Key", "key")
	req.Header.Set("X-NDHIS-User", "doctor-a")
	req.Header.Set("X-NDHIS-Role", "doctor")
	res := httptest.NewRecorder()
	handler(res, req)
	if res.Code != 204 {
		t.Fatalf("expected 204, got %d", res.Code)
	}
}

func TestGuardRejectsNonDoctorRole(t *testing.T) {
	s := &server{
		cfg:       config{DemoKey: "key", RequestsPerMin: 2, MaxBodyBytes: 1024},
		rates:     map[string]rateState{},
		semaphore: make(chan struct{}, 1),
	}
	handler := s.guard(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-NDHIS-Demo-Key", "key")
	req.Header.Set("X-NDHIS-User", "admin-a")
	req.Header.Set("X-NDHIS-Role", "admin")
	res := httptest.NewRecorder()
	handler(res, req)
	if res.Code != 403 {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}
