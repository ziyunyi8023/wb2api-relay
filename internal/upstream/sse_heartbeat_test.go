package upstream

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// delayedBody 构造「先静默 d、再一次性吐完 body」的上游 reader：模拟上游首字节前的
// 空窗——心跳存在的唯一理由就是这个窗口。
func delayedBody(d time.Duration, body string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		time.Sleep(d)
		_, _ = io.WriteString(pw, body)
		_ = pw.Close()
	}()
	return pr
}

// drippingBody 逐块吐帧、块间留 gap：给心跳留出与真实帧并发写的机会（交错检测用）。
func drippingBody(chunks []string, gap time.Duration) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		for _, c := range chunks {
			_, _ = io.WriteString(pw, c)
			time.Sleep(gap)
		}
		_ = pw.Close()
	}()
	return pr
}

const hbSampleStream = "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
	"data: [DONE]\n\n"

// stripHeartbeat 剥掉心跳注释帧：用于断言「心跳不改变任何真实字节」。
func stripHeartbeat(s string) string { return strings.ReplaceAll(s, ": hb\n\n", "") }

// 静默窗口内必须先出现心跳、且注释帧排在首个 data: 帧之前，真实帧与 [DONE] 语义不变。
func TestStreamHeartbeatFillsSilentGap(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := StreamHint(rec, delayedBody(250*time.Millisecond, hbSampleStream), nil, WithHeartbeat(30*time.Millisecond)); err != nil {
		t.Fatalf("StreamHint err=%v", err)
	}
	out := rec.Body.String()
	hb := strings.Index(out, ": hb\n\n")
	first := strings.Index(out, "data: ")
	if hb < 0 {
		t.Fatalf("silent gap must be filled with heartbeat; out=%q", out)
	}
	if first < 0 || hb > first {
		t.Fatalf("heartbeat must precede first data frame: hb=%d data=%d out=%q", hb, first, out)
	}
	if n := strings.Count(out, "[DONE]"); n != 1 {
		t.Fatalf("[DONE] count=%d want 1; out=%q", n, out)
	}
	if !strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with [DONE]; out=%q", out)
	}
	// 逐字节等价：剥掉注释帧后与不开心跳的输出完全一致。
	plain := httptest.NewRecorder()
	if err := StreamHint(plain, strings.NewReader(hbSampleStream), nil); err != nil {
		t.Fatalf("plain StreamHint err=%v", err)
	}
	if got, want := stripHeartbeat(out), plain.Body.String(); got != want {
		t.Fatalf("heartbeat changed real bytes:\n got=%q\nwant=%q", got, want)
	}
}

// 不挂 WithHeartbeat 时一个注释帧都不许写（缺省零行为变化）。
func TestStreamHeartbeatOffByDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := StreamHint(rec, delayedBody(120*time.Millisecond, hbSampleStream), nil); err != nil {
		t.Fatalf("StreamHint err=%v", err)
	}
	out := rec.Body.String()
	if strings.Contains(out, ": hb") {
		t.Fatalf("heartbeat must be opt-in; out=%q", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("stream truncated; out=%q", out)
	}
}

// 上游 error 帧路径：注释帧不干扰透传原文，错误帧仍计入有效帧、[DONE] 仍恰好一个。
func TestStreamHeartbeatErrorFramePath(t *testing.T) {
	errFrame := "{\"error\":{\"code\":\"6004\",\"msg\":\"too many requests\"}}"
	var seen string
	rec := httptest.NewRecorder()
	body := "data: " + errFrame + "\n\ndata: [DONE]\n\n"
	if err := StreamHint(rec, delayedBody(150*time.Millisecond, body), nil,
		WithHeartbeat(25*time.Millisecond),
		WithErrorFrameObserver(func(payload string) { seen = payload })); err != nil {
		t.Fatalf("StreamHint err=%v", err)
	}
	out := rec.Body.String()
	if seen != errFrame {
		t.Fatalf("observer payload changed: %q want %q", seen, errFrame)
	}
	if !strings.Contains(out, "data: "+errFrame+"\n\n") {
		t.Fatalf("error frame must pass through verbatim; out=%q", out)
	}
	if strings.Index(out, ": hb") > strings.Index(out, "data: ") {
		t.Fatalf("heartbeat must fill the silent gap before the error frame; out=%q", out)
	}
	if n := strings.Count(out, "[DONE]"); n != 1 {
		t.Fatalf("[DONE] count=%d want 1; out=%q", n, out)
	}
}

// 空流判定不受心跳影响：心跳写满整个静默期，validFrames 仍为 0，兜底 error 帧与
// errEmptyStream 语义必须照旧。
func TestStreamHeartbeatEmptyStreamStillDetected(t *testing.T) {
	rec := httptest.NewRecorder()
	err := StreamHint(rec, delayedBody(150*time.Millisecond, "data: [DONE]\n\n"), nil, WithHeartbeat(25*time.Millisecond))
	if !IsEmptyStreamError(err) {
		t.Fatalf("empty stream must still be reported, err=%v", err)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "empty upstream stream") {
		t.Fatalf("fallback error frame missing; out=%q", out)
	}
	if strings.Count(out, "[DONE]") != 1 {
		t.Fatalf("[DONE] count=%d want 1; out=%q", strings.Count(out, "[DONE]"), out)
	}
	if !strings.Contains(out, ": hb") {
		t.Fatalf("heartbeat expected during the silent gap; out=%q", out)
	}
}

// 并发写完整性：心跳 ticker 与读循环同时写，出站字节流里不能出现帧交错
// （每一行要么是 \": hb\" 注释、要么是 \"data: ...\" 帧）。
func TestStreamHeartbeatNoFrameInterleaving(t *testing.T) {
	chunks := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		chunks = append(chunks, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
	}
	chunks = append(chunks, "data: [DONE]\n\n")
	rec := httptest.NewRecorder()
	if err := StreamHint(rec, drippingBody(chunks, 2*time.Millisecond), nil, WithHeartbeat(time.Millisecond)); err != nil {
		t.Fatalf("StreamHint err=%v", err)
	}
	out := rec.Body.String()
	frames, hbs := 0, 0
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case ln == "":
		case ln == ": hb":
			hbs++
		case strings.HasPrefix(ln, "data: "):
			frames++
		default:
			t.Fatalf("corrupted line (frame interleaving?): %q", ln)
		}
	}
	if frames != 41 { // 40 真实帧 + [DONE]
		t.Fatalf("frame count=%d want 41; out=%q", frames, out)
	}
	if hbs == 0 {
		t.Fatal("heartbeat never fired under drip feed")
	}
}
