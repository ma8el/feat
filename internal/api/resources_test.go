package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestAnUnmeasuredFigureIsNullRatherThanZero pins the shape of a report from a
// machine that could not answer.
//
// It is the wire half of the rule ADR-028 established for diagnostics: a value
// nothing measured is never published as one. A client reading zero would draw a
// dashboard claiming a machine with no free memory and tasks using no processor,
// which is worse than a dashboard with gaps in it.
func TestAnUnmeasuredFigureIsNullRatherThanZero(t *testing.T) {
	service := newFakeService()
	service.resources = ResourceReport{
		Machine: MachineResources{Cores: 8},
		Tasks:   []TaskResources{{TaskID: "7f3a1c2e-9b4d-4c81-8f2a-1d6b0c5e7a93"}},
		Notes:   []string{"machine load is unavailable: vm.loadavg reported nothing"},
		Sampled: true,
	}

	response := request(t, NewHandler(Options{Service: service}), http.MethodGet, "/v1/resources")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}

	body := response.Body.String()
	for _, field := range []string{
		`"load": null`, `"memory": null`, `"disk": null`,
		`"cpu_percent": null`, `"memory_bytes": null`,
	} {
		if !strings.Contains(body, field) {
			t.Errorf("a figure nothing measured is not null: want %s in\n%s", field, body)
		}
	}
}

// TestAReportWithNoSampleIsStillAReport checks that a daemon which has not
// sampled yet answers rather than failing.
//
// It is the state of every session's first seconds, and a dashboard that could
// not draw during them would be a dashboard that flickered on every start.
func TestAReportWithNoSampleIsStillAReport(t *testing.T) {
	service := newFakeService()
	service.resources = ResourceReport{Notes: []string{"no resource sample has been taken yet"}}

	response := request(t, NewHandler(Options{Service: service}), http.MethodGet, "/v1/resources")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}

	var report ResourceReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatalf("decoding the report: %v", err)
	}
	if report.Sampled {
		t.Error("a daemon that has taken no sample reports one")
	}
	if len(report.Notes) == 0 {
		t.Error("a report with no sample says nothing about why")
	}
	if report.Tasks == nil {
		t.Error("the task list is null rather than empty, so a client has to check for it")
	}
	if !report.CollectedAt.IsZero() {
		t.Error("a report with no sample carries a collection time")
	}
}
