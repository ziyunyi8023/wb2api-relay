// diagnose.go 内容拦截诊断：只读，不改请求。
//
// 为什么需要（2026-11-19）：上游 11-128「unapproved channel」按 system/assistant
// 里的固定台词整轮拒绝，但日志只记了上游返回体，没记**我们发出去的什么内容**
// 触发的。结果每遇到一个新指纹只能靠猜（当次实测：已知规则补完后仍有 1 次复现，
// 无法从日志定位是哪句话）。
//
// 这里只取 system 消息开头若干字符写进 WARN 日志：system 通常是客户端模板，
// 不含用户问答内容；user/assistant 正文一律不落日志。
package server

import (
	"encoding/json"
	"strings"
)

// systemHeadMaxRunes 诊断日志里 system 开头的最大字符数（按 rune 截，避免切碎中文）。
const systemHeadMaxRunes = 100

// systemHead 取请求体里第一条 system/developer 消息的开头，用于定位被拦指纹。
// 解析失败或无 system 时返回可读标记（不返回空串，避免日志里"看起来没事"）。
func systemHead(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "(body 解析失败)"
	}
	for _, m := range req.Messages {
		if m.Role != "system" && m.Role != "developer" {
			continue
		}
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			return "(system content 非字符串)"
		}
		r := []rune(s)
		if len(r) > systemHeadMaxRunes {
			return string(r[:systemHeadMaxRunes]) + "…"
		}
		return string(r)
	}
	return "(无 system 消息)"
}

// assistantFingerprintHint 在 system 没有线索时，扫 assistant 消息里是否出现
// 已知的 agent 身份句形状——返回命中的那一小段，便于对照上游黑名单。
// 找不到返回空串（调用方据此不追加日志）。
func assistantFingerprintHint(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			continue
		}
		if i := strings.Index(s, "You are an AI agent"); i >= 0 {
			end := i + 80
			if end > len(s) {
				end = len(s)
			}
			return strings.ReplaceAll(s[i:end], "\n", " ")
		}
	}
	return ""
}
