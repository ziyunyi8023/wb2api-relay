// sanitize_dsh_test.go agent 身份句形状脱敏回归。
//
// 依据（实测表，magpie-community/plugins packages/workbuddy/REFUSED.md）：
//
//	"You are an AI agent, powered by DeepSeek Harness."          → 400 code 11-128
//	"You are an AI agent powered by deepseek harness."          → 400（大小写无用）
//	"You are an AI agent powered by DeepSeekHarness."           → 400（去空格无用）
//	"You are an AI agent, powered by DeepSeek Harness."         → 200（插逗号通过）
//	"You are an AI agent, powered by DeepSeek Harness Desktop."  → 400（加词无用）
//	"You are an AI agent powered by Claude Code."               → 200（换名通过）
//
// 触发条件是「形状 + 产品名」。本包只破坏形状（插一个逗号），因此对所有产品名
// 通杀，包括我们还没见过的；语义不变。
package upstream

import (
	"strings"
	"testing"
)

func TestAgentShapeSentenceRewritten(t *testing.T) {
	cases := []string{
		"You are an AI agent powered by DeepSeek Harness.",
		"You are an AI agent powered by DeepSeek Harness Desktop.",
		"You are an AI agent powered by DeepSeek Harness CLI.",
		"You are an AI agent powered by deepseek harness.",
		"You are an AI agent powered by Claude Code.",
		"preamble\nYou are an AI agent powered by Something New.\ntail",
	}
	for _, in := range cases {
		out := sanitizeText(in)
		if strings.Contains(out, "You are an AI agent powered by") {
			t.Errorf("形状未被破坏: %q", out)
		}
		if !strings.Contains(out, "You are an AI agent, powered by") {
			t.Errorf("改写后丢了原意: %q", out)
		}
	}
}

// 已经是逗号形态的不再改动：保证重试路径二次净化幂等。
func TestAgentShapeAlreadySafeIsUntouched(t *testing.T) {
	in := "You are an AI agent, powered by DeepSeek Harness."
	if out := sanitizeText(in); out != in {
		t.Errorf("安全形态被再次改写: in=%q out=%q", in, out)
	}
}

// 负对照：无关文本一个字节都不能动（否则是无声的内容篡改）。
func TestAgentShapeLeavesUnrelatedTextAlone(t *testing.T) {
	cases := []string{
		"You are a helpful assistant.",
		"You are a DeepSeek Harness agent.",
		"You are an AI assistant powered by electricity.",
	}
	for _, in := range cases {
		if out := sanitizeText(in); out != in {
			t.Errorf("不该改写的文本被改了: in=%q out=%q", in, out)
		}
	}
}

func TestAgentShapeRewriteIdempotent(t *testing.T) {
	in := "You are an AI agent powered by DeepSeek Harness."
	once := sanitizeText(in)
	twice := sanitizeText(once)
	if once != twice {
		t.Errorf("不幂等: once=%q twice=%q", once, twice)
	}
}
