package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var supportedFacilities = []string{"JNF"}
var supportedDepartments = []string{"A&E", "Outpatient", "Medical Ward", "Surgical Ward", "Pediatrics"}
var supportedDiseases = []string{"respiratory", "gastro", "diabetes", "hypertension"}
var resolutionCodePattern = regexp.MustCompile(`^\d+(?:ms|s|min|h|d|w|mo|y)$`)

type toolHTTPError struct {
	Status int
	Detail string
}

func (e *toolHTTPError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("tool status %d", e.Status)
	}
	return fmt.Sprintf("tool status %d: %s", e.Status, e.Detail)
}

func canonicalFrom(value string, allowed []string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, candidate := range allowed {
		if strings.EqualFold(value, candidate) {
			return candidate, true
		}
	}
	return "", false
}

func canonicalFacility(value string) (string, bool) { return canonicalFrom(value, supportedFacilities) }
func canonicalDepartment(value string) (string, bool) { return canonicalFrom(value, supportedDepartments) }
func canonicalDisease(value string) (string, bool) { return canonicalFrom(value, supportedDiseases) }

func validateToolArguments(name string, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	clean := map[string]any{}
	requireString := func(key string, max int) (string, error) {
		value, ok := args[key].(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" || len([]rune(value)) > max {
			return "", fmt.Errorf("%s requires a valid %s", name, key)
		}
		return value, nil
	}

	forecastCommon := func() error {
		facilityRaw, err := requireString("facility", 80)
		if err != nil {
			return err
		}
		facility, ok := canonicalFacility(facilityRaw)
		if !ok {
			return fmt.Errorf("unsupported facility %q; available: %s", facilityRaw, strings.Join(supportedFacilities, ", "))
		}
		clean["facility"] = facility

		if rawDays, ok := numberAsInt(args["horizon_days"]); ok {
			if rawDays < 1 || rawDays > 730 {
				return fmt.Errorf("horizon_days must be between 1 and 730")
			}
			clean["horizon"] = rawDays
			clean["horizon_unit"] = "days"
		} else {
			horizon, ok := numberAsFloat(args["horizon"])
			if !ok || horizon <= 0 {
				return fmt.Errorf("%s requires a positive horizon", name)
			}
			unit, ok := args["horizon_unit"].(string)
			unit = normalizeHorizonUnit(unit)
			if !ok || unit == "" {
				return fmt.Errorf("%s requires a supported horizon_unit", name)
			}
			if horizonSeconds(horizon, unit) > 2*365.25*24*3600+86400 {
				return fmt.Errorf("forecast horizon is limited to 2 years")
			}
			clean["horizon"] = horizon
			clean["horizon_unit"] = unit
		}

		resolution := "auto"
		if value, ok := args["resolution"].(string); ok && strings.TrimSpace(value) != "" {
			resolution = strings.ToLower(strings.TrimSpace(value))
			if resolution != "auto" && !resolutionCodePattern.MatchString(resolution) {
				return fmt.Errorf("invalid resolution %q", resolution)
			}
		}
		clean["resolution"] = resolution
		if asOf, ok := args["as_of"].(string); ok && strings.TrimSpace(asOf) != "" {
			clean["as_of"] = strings.TrimSpace(asOf)
		}
		clean["include_actuals"] = true
		return nil
	}

	switch name {
	case "forecast_patient_volume", "forecast_bed_occupancy":
		if err := forecastCommon(); err != nil {
			return nil, err
		}
		departmentRaw, err := requireString("department", 80)
		if err != nil {
			return nil, err
		}
		department, ok := canonicalDepartment(departmentRaw)
		if !ok {
			return nil, fmt.Errorf("unsupported department %q; available: %s", departmentRaw, strings.Join(supportedDepartments, ", "))
		}
		clean["department"] = department
	case "forecast_disease_incidence":
		if err := forecastCommon(); err != nil {
			return nil, err
		}
		diseaseRaw, err := requireString("disease", 120)
		if err != nil {
			return nil, err
		}
		disease, ok := canonicalDisease(diseaseRaw)
		if !ok {
			return nil, fmt.Errorf("unsupported disease %q; available: %s", diseaseRaw, strings.Join(supportedDiseases, ", "))
		}
		clean["disease"] = disease
		if departmentRaw, ok := args["department"].(string); ok && strings.TrimSpace(departmentRaw) != "" {
			department, valid := canonicalDepartment(departmentRaw)
			if !valid {
				return nil, fmt.Errorf("unsupported department %q; available: %s", departmentRaw, strings.Join(supportedDepartments, ", "))
			}
			clean["department"] = department
		}
	case "get_radiology_result":
		resultID, err := requireString("result_id", 160)
		if err != nil {
			return nil, err
		}
		clean["result_id"] = resultID
	case "get_service_status":
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return clean, nil
}

func normalizeHorizonUnit(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ms", "millisecond", "milliseconds":
		return "milliseconds"
	case "s", "sec", "second", "seconds":
		return "seconds"
	case "m", "min", "minute", "minutes":
		return "minutes"
	case "h", "hr", "hour", "hours":
		return "hours"
	case "d", "day", "days":
		return "days"
	case "w", "week", "weeks":
		return "weeks"
	case "mo", "month", "months":
		return "months"
	case "y", "yr", "year", "years":
		return "years"
	default:
		return ""
	}
}

func horizonSeconds(value float64, unit string) float64 {
	scale := map[string]float64{
		"milliseconds": 0.001,
		"seconds":      1,
		"minutes":      60,
		"hours":        3600,
		"days":         86400,
		"weeks":        604800,
		"months":       2629800,
		"years":        31557600,
	}[unit]
	return value * scale
}

func numberAsInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		integer := int(typed)
		return integer, typed == float64(integer)
	case int:
		return typed, true
	case json.Number:
		integer, err := typed.Int64()
		return int(integer), err == nil
	default:
		return 0, false
	}
}

func numberAsFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *server) executeTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	validated, err := validateToolArguments(name, args)
	if err != nil {
		return nil, err
	}
	switch name {
	case "forecast_patient_volume":
		validated["metric"] = "patient_arrivals"
		return s.postJSON(ctx, s.cfg.ForecastURL+"/forecast", validated)
	case "forecast_bed_occupancy":
		validated["metric"] = "bed_occupancy"
		return s.postJSON(ctx, s.cfg.ForecastURL+"/forecast", validated)
	case "forecast_disease_incidence":
		validated["metric"] = "disease_incidence"
		return s.postJSON(ctx, s.cfg.ForecastURL+"/forecast", validated)
	case "get_radiology_result":
		return s.getJSON(ctx, s.cfg.RadiologyURL+"/results/"+url.PathEscape(validated["result_id"].(string)))
	case "get_service_status":
		return json.Marshal(map[string]any{"gateway": "ready", "processing": "local", "services": s.serviceStatus(ctx)})
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *server) postJSON(ctx context.Context, endpoint string, value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxToolResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeToolHTTPError(resp.StatusCode, payload)
	}
	if !json.Valid(payload) {
		return nil, errors.New("tool returned invalid JSON")
	}
	return payload, nil
}

func (s *server) getJSON(ctx context.Context, endpoint string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxToolResponse))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeToolHTTPError(resp.StatusCode, payload)
	}
	if !json.Valid(payload) {
		return nil, errors.New("tool returned invalid JSON")
	}
	return payload, nil
}

func decodeToolHTTPError(status int, payload []byte) error {
	var decoded struct {
		Detail string `json:"detail"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(payload, &decoded)
	detail := strings.TrimSpace(decoded.Detail)
	if detail == "" {
		detail = strings.TrimSpace(decoded.Error)
	}
	if detail == "" {
		detail = strings.TrimSpace(string(payload))
	}
	if len(detail) > 600 {
		detail = detail[:600] + "…"
	}
	return &toolHTTPError{Status: status, Detail: detail}
}

func (s *server) forecastProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.ForecastURL+"/forecast")
}

func (s *server) forecastCapabilitiesProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.ForecastURL+"/capabilities")
}

func (s *server) forecastHistoryProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.ForecastURL+"/history")
}

func (s *server) radiologyProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHTTP(w, r, s.cfg.RadiologyURL+"/analyze")
}

func (s *server) radiologyResultProxy(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || len(id) > 160 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid result id"})
		return
	}
	s.proxyHTTP(w, r, s.cfg.RadiologyURL+"/results/"+url.PathEscape(id))
}

func (s *server) proxyHTTP(w http.ResponseWriter, r *http.Request, endpoint string) {
	started := time.Now()
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	w.Header().Set("X-NDHIS-Request-ID", requestID)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint, r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		s.audit(r, requestID, r.URL.Path, "", http.StatusBadRequest, started)
		return
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		s.audit(r, requestID, r.URL.Path, "", http.StatusBadGateway, started)
		return
	}
	defer resp.Body.Close()
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, s.cfg.MaxBodyBytes))
	s.audit(r, requestID, r.URL.Path, "", resp.StatusCode, started)
}
