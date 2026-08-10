package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTunnelManagerChecksHandlerValidatesMethodAndBody(t *testing.T) {
	s := newServerTestServer(t)

	getRec := httptest.NewRecorder()
	s.handleTunnelManagerChecks(getRec, httptest.NewRequest(http.MethodGet, "/api/tunnel-manager/checks", nil))
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", getRec.Code)
	}

	badRec := httptest.NewRecorder()
	s.handleTunnelManagerChecks(badRec, httptest.NewRequest(http.MethodPost, "/api/tunnel-manager/checks", strings.NewReader(`{"indexes":`)))
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body status = %d: %s", badRec.Code, badRec.Body.String())
	}

	disabledRec := httptest.NewRecorder()
	s.handleTunnelManagerChecks(disabledRec, httptest.NewRequest(http.MethodPost, "/api/tunnel-manager/checks", strings.NewReader(`{"indexes":[]}`)))
	if disabledRec.Code != http.StatusConflict {
		t.Fatalf("disabled manager status = %d: %s", disabledRec.Code, disabledRec.Body.String())
	}
}
