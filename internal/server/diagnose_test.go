// diagnose_test.go 内容拦截诊断的回归：判据必须对着真实请求体形状校准。
package server

import (
	"strings"
	"testing"
)

func TestSystemHead(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"system","content":"You are an AI agent, powered by DeepSeek Harness."},
		{"role":"user","content":"hi"}]}`)
	got := systemHead(body)
	if !strings.Contains(got, "You are an AI agent") {
		t.Fatalf("systemHead=%q，应含 system 开头", got)
	}
}

func TestSystemHeadDeveloperRole(t *testing.T) {
	// OpenAI 新版用 developer 代替 system，同样要能取到。
	body := []byte(`{"messages":[{"role":"developer","content":"rules here"},{"role":"user","content":"hi"}]}`)
	if got := systemHead(body); got != "rules here" {
		t.Fatalf("systemHead=%q want rules here", got)
	}
}

func TestSystemHeadMarkers(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"无 system", `{"messages":[{"role":"user","content":"hi"}]}`, "(无 system 消息)"},
		{"坏 JSON", `{bad`, "(body 解析失败)"},
		{"非字符串 system", `{"messages":[{"role":"system","content":[{"type":"text","text":"x"}]}]}`, "(system content 非字符串)"},
	}
	for _, c := range cases {
		if got := systemHead([]byte(c.body)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestSystemHeadTruncatesByRune(t *testing.T) {
	// 中文按 rune 截，不能切出半个字（错位会让日志变成乱码）。
	long := strings.Repeat("中文", 80)
	body := []byte(`{"messages":[{"role":"system","content":"` + long + `"}]}`)
	got := systemHead(body)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("超长 system 应被截断并以省略号结尾: %q", got)
	}
	if !strings.HasPrefix(got, "中文") {
		t.Fatalf("截断位置错乱: %q", got)
	}
}

func TestAssistantFingerprintHint(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"intro\\nYou are an AI agent powered by X.\\ntail"}]}`)
	got := assistantFingerprintHint(body)
	if !strings.Contains(got, "You are an AI agent") {
		t.Fatalf("应命中 assistant 身份句，got=%q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("提示串不应带换行: %q", got)
	}
}

func TestAssistantFingerprintHintAbsent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":"nothing special"}]}`)
	if got := assistantFingerprintHint(body); got != "" {
		t.Fatalf("无命中时应返回空串，got=%q", got)
	}
}
