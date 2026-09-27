package tracker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStubAnnounceHandler_ReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/announce?info_hash=abc", nil)
	rr := httptest.NewRecorder()
	stubAnnounceHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestStubAnnounceHandler_PostReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/announce", nil)
	rr := httptest.NewRecorder()
	stubAnnounceHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

