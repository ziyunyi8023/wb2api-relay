// cross_domain_fallback_explicit_test.go 显式前缀跨域回退开关
// （pool.cross_domain_fallback_explicit）的行为测试。
//
// 上游 issue #135 的语义是「显式 cn:/global: 前缀永不回退」（由
// TestExplicitRealmNeverFallsBack 守着）。本开关是它的可选放行位：默认 false 时
// 行为与上游逐字节一致；打开后，带前缀的请求在本域该模型选不到号时允许换域顶替，
// 用于「一个域的模型配额被打满就整域瘫」的场景。
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
)

// newExplicitRealmPool 同 newRealmPool，但用 SetStrategy 无关的确定性单号池：
// 只给 global 一个号，cn 号故意不提供，用于验证「cn: 前缀在本域无号时回退」。
func newExplicitRealmPool(t *testing.T) *pool.Pool {
	t.Helper()
	p := testPoolWith(
		&auth.Auth{UID: "cn1", AccessToken: "at-cn", ExpiresAt: 9999999999},
		&auth.Auth{UID: "gl1", AccessToken: "at-gl", Domain: "www.workbuddy.ai", ExpiresAt: 9999999999},
	)
	p.SetCredits("cn1", 2000, 0)
	p.SetCredits("gl1", 1000, 0)
	return p
}

// 开关缺省必须为 false（与上游开关一样是零回归默认）。
func TestCrossDomainFallbackExplicitDefaultOff(t *testing.T) {
	p := newExplicitRealmPool(t)
	if p.CrossDomainFallbackExplicit() {
		t.Fatal("cross_domain_fallback_explicit 缺省必须为 false")
	}
	p.SetCrossDomainFallbackExplicit(true)
	if !p.CrossDomainFallbackExplicit() {
		t.Fatal("SetCrossDomainFallbackExplicit(true) 未生效")
	}
}

// 打开「显式前缀也回退」后：cn: 前缀的请求在 cn 号 11102 后改用 global 号，拿到 200。
func TestExplicitRealmFallsBackWhenSwitchOn(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		if authz == "Bearer at-cn" {
			return 400, `{"code":11102,"msg":"model [deepseek-v4.1-flash-sg] service info not found"}`, false
		}
		return 200, sseOK, true
	})
	p := newExplicitRealmPool(t)
	p.SetCrossDomainFallback(true)
	p.SetCrossDomainFallbackExplicit(true)
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"cn:deepseek-v4.1-flash-sg","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s (want 200: 显式前缀在开关打开后必须回退 global)", rec.Code, rec.Body)
	}
	if calls["Bearer at-cn"] != 1 || calls["Bearer at-gl"] != 1 {
		t.Errorf("calls=%v want cn 1 次（11102）→ global 1 次（回退成功）", calls)
	}
}

// 只开父开关、不开显式开关：显式前缀仍不回退（上游语义不变）。
func TestExplicitRealmNoFallbackWhenSwitchOff(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		if authz == "Bearer at-cn" {
			return 400, `{"code":11102,"msg":"model [deepseek-v4.1-flash-sg] service info not found"}`, false
		}
		return 200, sseOK, true
	})
	p := newExplicitRealmPool(t)
	p.SetCrossDomainFallback(true)
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"cn:deepseek-v4.1-flash-sg","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code == http.StatusOK {
		t.Fatalf("code=%d 显式前缀在关掉 explicit 开关时不得回退", rec.Code)
	}
	if calls["Bearer at-gl"] != 0 {
		t.Fatalf("calls=%v global 号不得被调用", calls)
	}
}
