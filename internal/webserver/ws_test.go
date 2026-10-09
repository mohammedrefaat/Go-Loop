package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebSocketHandler_NewWebSocketHandler(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	pm := NewPairingManager(0)

	handler := NewWebSocketHandler(sm, pm)
	if handler == nil {
		t.Fatal("NewWebSocketHandler should not return nil")
	}
	if handler.sessionManager != sm {
		t.Error("session manager should be set")
	}
	if handler.pairingManager != pm {
		t.Error("pairing manager should be set")
	}
	if handler.upgrader == nil {
		t.Error("upgrader should be set")
	}
}

func TestWebSocketHandler_HandleWebSocket_MissingSessionID(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	pm := NewPairingManager(0)

	handler := NewWebSocketHandler(sm, pm)

	req := httptest.NewRequest(http.MethodGet, "/ws/", nil)
	w := httptest.NewRecorder()

	handler.HandleWebSocket(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestWebSocketHandler_HandleWebSocket_MissingToken(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	pm := NewPairingManager(0)

	handler := NewWebSocketHandler(sm, pm)

	req := httptest.NewRequest(http.MethodGet, "/ws/test-session", nil)
	w := httptest.NewRecorder()

	handler.HandleWebSocket(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestWebSocketHandler_HandleWebSocket_UnapprovedToken(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	pm := NewPairingManager(0)

	handler := NewWebSocketHandler(sm, pm)

	req := httptest.NewRequest(http.MethodGet, "/ws/test-session?token=unapproved-token", nil)
	w := httptest.NewRecorder()

	handler.HandleWebSocket(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for unapproved token, got %d", w.Code)
	}
}

func TestWebSocketHandler_HandleWebSocket_ApprovedToken_InvalidUpgrade(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	pm := NewPairingManager(5 * time.Minute)

	code, token, err := pm.CreatePair(".")
	if err != nil {
		t.Fatalf("failed to create pair: %v", err)
	}
	if _, err := pm.Approve(code); err != nil {
		t.Fatalf("failed to approve pair: %v", err)
	}

	handler := NewWebSocketHandler(sm, pm)

	req := httptest.NewRequest(http.MethodGet, "/ws/test-session?token="+token, nil)
	w := httptest.NewRecorder()

	handler.HandleWebSocket(w, req)

	// Since upgrade fails (not a real websocket connection), auth should pass and proceed to upgrade
	if w.Code == http.StatusUnauthorized {
		t.Errorf("expected approved token to pass auth, got 401")
	}
}
