package auth

import (
	"encoding/base64"
	"testing"
)

// mustJWT 用给定 payload 拼一个最小 JWT（header.payload.signature）。
// IsEnterpriseSession 不校验签名，只解 payload，故签名段可为任意占位。
func mustJWT(t *testing.T, payload string) string {
	t.Helper()
	return "hdr." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
}

// TestIsEnterpriseSession 企业会话判定：**只看 enterpriseId**（真企业版）。
//
// token_source=enterprise_switch 曾被视为「企业切换会话」判据（commit 9282779），
// 2026-11-19 实测推翻：13 个账号的 access + refresh token 全部带此声明，授权时刻
// 分散在 6 个不同时间点（11-16 22:00 / 11-18 14:16 / 15:38 / 15:41 / 15:43 / 22:00），
// 且 EnterpriseID 全为空。13 次独立授权不可能次次撞上「企业切换状态」——该声明是
// platform=CLI 授权通道的常态输出。若仍据此门控，会把全部 CLI 账号误判为零收益，
// 表现为成长任务/签到/旅行/夜猫子/活跃上报被静默跳过（2026-11-19 线上实际故障）。
func TestIsEnterpriseSession(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"enterprise_switch 不再单独触发", mustJWT(t, `{"sub":"u1","token_source":"enterprise_switch"}`), false},
		{"无 token_source 声明", mustJWT(t, `{"sub":"u1","scope":"openid profile"}`), false},
		{"其他 token_source", mustJWT(t, `{"sub":"u1","token_source":"password"}`), false},
		{"带填充 base64url 同样不触发", "hdr." + base64.URLEncoding.EncodeToString([]byte(`{"token_source":"enterprise_switch"}`)) + ".sig", false},
		{"payload 非 JSON", "hdr." + base64.RawURLEncoding.EncodeToString([]byte("not-json")) + ".sig", false},
		{"非 JWT 形态", "opaque-token", false},
		{"空 token", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Auth{UID: "u1", AccessToken: c.token}
			if got := a.IsEnterpriseSession(); got != c.want {
				t.Errorf("IsEnterpriseSession()=%v want %v", got, c.want)
			}
		})
	}

	t.Run("enterpriseId 非空短路为真", func(t *testing.T) {
		a := &Auth{UID: "u1", EnterpriseID: "e1", AccessToken: mustJWT(t, `{"sub":"u1"}`)}
		if !a.IsEnterpriseSession() {
			t.Error("IsEnterprise()=true 时 IsEnterpriseSession() 应恒为 true")
		}
	})

	t.Run("企业切换会话不影响 IsEnterprise（计费口径隔离）", func(t *testing.T) {
		a := &Auth{UID: "u1", AccessToken: mustJWT(t, `{"token_source":"enterprise_switch"}`)}
		if a.IsEnterprise() {
			t.Error("仅 token_source=enterprise_switch 不应翻转 IsEnterprise（额度查询仍走个人口径）")
		}
	})
}
