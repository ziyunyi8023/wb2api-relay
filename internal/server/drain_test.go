package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDrainNotWired Drain 未注入时返回 501（可运维区分「未接线」与「失败」）。
func TestDrainNotWired(t *testing.T) {
	h := NewHandler(Config{})
	req := httptest.NewRequest("POST", "/debug/drain", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("code=%d want 501", w.Code)
	}
}

// TestDrainTriggersCallback 接线后：202 + {"status":"draining"}，异步触发 Drain 回调。
func TestDrainTriggersCallback(t *testing.T) {
	called := make(chan struct{}, 1)
	h := NewHandler(Config{Drain: func() { called <- struct{}{} }})
	req := httptest.NewRequest("POST", "/debug/drain", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("code=%d want 202", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["status"] != "draining" {
		t.Fatalf("status=%q want draining", body["status"])
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("Drain callback not invoked")
	}
}

// TestDrainRequiresAuth 配置 api_key 后 /debug/drain 必须带 Bearer。
func TestDrainRequiresAuth(t *testing.T) {
	h := NewHandler(Config{APIKey: "secret", Drain: func() {}})
	req := httptest.NewRequest("POST", "/debug/drain", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth code=%d want 401", w.Code)
	}

	req2 := httptest.NewRequest("POST", "/debug/drain", nil)
	req2.Header.Set("Authorization", "Bearer wrong")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("bad-key code=%d want 401", w2.Code)
	}

	req3 := httptest.NewRequest("POST", "/debug/drain", nil)
	req3.Header.Set("Authorization", "Bearer secret")
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, req3)
	if w3.Code != http.StatusAccepted {
		t.Fatalf("good-key code=%d want 202 body=%s", w3.Code, strings.TrimSpace(w3.Body.String()))
	}
}
