package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

func TestConversationIDRequiredAcrossRotation(t *testing.T) {
	for _, tc := range []struct{ name, body, header, want string }{
		{"missing", `{"model":"glm-5.2","metadata":{"user_id":"u"},"messages":[{"role":"user","content":"hello"}]}`, "", ""},
		{"header", `{"model":"glm-5.2","messages":[]}`, "client-header", "client-header"},
		{"body-priority", `{"model":"glm-5.2","conversationId":"client-body","messages":[]}`, "client-header", "client-body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ids []string
			up := &upstream.Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				ids = append(ids, r.Header.Get("X-Conversation-ID"))
				status, body, ct := 200, sseOK, "text/event-stream"
				if len(ids) == 1 {
					status, body, ct = 429, `{"code":429,"msg":"rate limit"}`, "application/json"
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			p := testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999}, &auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999})
			h := NewHandler(Config{Pool: p, Upstream: up, MaxRotate: 2})
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(tc.body))
			req.Header.Set("X-Conversation-ID", tc.header)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 || len(ids) != 2 {
				t.Fatalf("status=%d attempts=%d body=%s", rec.Code, len(ids), rec.Body.String())
			}
			if ids[0] == "" || ids[0] != ids[1] {
				t.Fatalf("missing or unstable conversation IDs: %q", ids)
			}
			if tc.want != "" && ids[0] != tc.want {
				t.Fatalf("id=%q want %q", ids[0], tc.want)
			}
		})
	}
}
