package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// 在途名额饱和（pool.max_in_flight）时选号返回 nil 的三条路径回归：
//   1) 名额稍后释放 → 等，请求成功（不再先吃一个 503）；
//   2) 等到 wait_timeout 仍满 → 429 + Retry-After + code=pool_saturated；
//   3) 号真的不可用（禁用/冷却）→ 不等，照旧 503 no_healthy_account。
// 生产事故背景：名额满即 503，客户端立刻重打，一次并发高峰被放大成重试风暴。

// okUpstream 返回总是成功吐 SSE 的上游 fake，并记录实际打了几次上游。
func okUpstream(calls *int) *upstream.Client {
	return &upstream.Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls++
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sseOK)),
		}, nil
	})}}
}

func chatReq() *http.Request {
	body := `{"model":"glm-5.2","messages":[{"role":"user","content":"hello"}]}`
	return httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
}

// TestPoolSaturatedWaitsForSlot 名额被占满但会释放：等名额，请求照常成功。
func TestPoolSaturatedWaitsForSlot(t *testing.T) {
	calls := 0
	p := testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999})
	p.SetMaxInFlight(1)
	if !p.Acquire("a") {
		t.Fatal("pre-acquire failed")
	}
	p.SetWaitTimeout(2 * time.Second)
	h := NewHandler(Config{Pool: p, Upstream: okUpstream(&calls), MaxRotate: 1})

	go func() {
		time.Sleep(300 * time.Millisecond)
		p.Release("a")
	}()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, chatReq())
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d want 1", calls)
	}
	if got := p.SaturatedCountFor("", "glm-5.2"); got != 0 {
		t.Fatalf("in-flight slot leaked: saturated=%d", got)
	}
}

// TestPoolSaturatedTimeout429 等到 wait_timeout 仍无名额：429 + Retry-After，
// 不再把「并发满」伪装成 no_healthy_account（那会让客户端立刻重打）。
func TestPoolSaturatedTimeout429(t *testing.T) {
	calls := 0
	p := testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999})
	p.SetMaxInFlight(1)
	if !p.Acquire("a") {
		t.Fatal("pre-acquire failed")
	}
	defer p.Release("a")
	p.SetWaitTimeout(300 * time.Millisecond)
	h := NewHandler(Config{Pool: p, Upstream: okUpstream(&calls), MaxRotate: 1})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, chatReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After=%q want 2", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "pool_saturated") || strings.Contains(body, "no_healthy_account") {
		t.Fatalf("unexpected code: %s", body)
	}
	if calls != 0 {
		t.Fatalf("upstream calls=%d want 0 (never reached upstream)", calls)
	}
}

// TestPoolDeadAccountFailsFast 池子里真的没有可用号：不等待，立刻 503 no_healthy_account。
func TestPoolDeadAccountFailsFast(t *testing.T) {
	calls := 0
	p := testPoolWith(&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999})
	p.SetMaxInFlight(1)
	p.Disable("a", "test")
	p.SetWaitTimeout(5 * time.Second)
	h := NewHandler(Config{Pool: p, Upstream: okUpstream(&calls), MaxRotate: 1})

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, chatReq())
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waited %s for a pool with no usable account", d)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no_healthy_account") {
		t.Fatalf("code changed: %s", rec.Body.String())
	}
}

// TestAcquireLoserDoesNotBurnRotation 复现 2026-10-24 生产 503 的真正成因：
// 账号被 Pick 选中但 CAS 抢在途名额抢输（名额被并发请求拿走），旧代码此时已经
// 把该号写进 tried 并消耗一格 MaxRotate，三轮全抢输 ⇒ 一次上游都没打就 503
// no_healthy_account（日志里 account 非空、duration_ms 是退避时长）。
//
// 判据：Acquire 抢输不得记 tried、不得消耗轮转预算 —— 号池有多个号时，
// 抢输后必须继续在同一格预算内找到有空的号并发上游一次，最终 200。
func TestAcquireLoserDoesNotBurnRotation(t *testing.T) {
	calls := 0
	// 三个号，每个名额 1。先把 a、b 的名额占满，只有 c 有空。
	p := testPoolWith(
		&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999},
		&auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999},
		&auth.Auth{UID: "c", AccessToken: "c", ExpiresAt: 9999999999},
	)
	p.SetMaxInFlight(1)
	for _, uid := range []string{"a", "b"} {
		if !p.Acquire(uid) {
			t.Fatalf("pre-acquire %s failed", uid)
		}
		defer p.Release(uid)
	}
	// MaxRotate=1：预算只够一次「选号 + 抢名额」。若抢输还要吃掉预算，
	// 这个请求必然 503；修好后它应落到有空名额的 c 上并成功。
	p.SetWaitTimeout(2 * time.Second)
	h := NewHandler(Config{Pool: p, Upstream: okUpstream(&calls), MaxRotate: 1})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, chatReq())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s（抢输吃掉了轮转预算）", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d want 1", calls)
	}
	// c 的名额必须已释放（heldUID 出口统一 Release），否则下一请求饿死。
	if got := p.SaturatedCountFor("", "glm-5.2"); got != 2 {
		t.Fatalf("in-flight after request: saturated=%d want 2 (only a,b held)", got)
	}
}

// TestPoolSaturatedConcurrentNo503 名额争夺战：N 个请求同时抢 M 个在途名额
// （N ≫ M），全部必须成功 —— 名额是「暂时被占」，用换号预算去数它会让
// MaxRotate=3 在名额竞争里提前耗尽，把并发压力翻成 503（2026-10-24 生产实测）。
func TestPoolSaturatedConcurrentNo503(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	up := &upstream.Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		// 慢一点，确保并发真的叠在一起抢占名额。
		time.Sleep(30 * time.Millisecond)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sseOK)),
		}, nil
	})}}

	// 3 个号 × 名额 1 = 总共 3 个在途槽，打 20 个并发。
	p := testPoolWith(
		&auth.Auth{UID: "a", AccessToken: "a", ExpiresAt: 9999999999},
		&auth.Auth{UID: "b", AccessToken: "b", ExpiresAt: 9999999999},
		&auth.Auth{UID: "c", AccessToken: "c", ExpiresAt: 9999999999},
	)
	p.SetMaxInFlight(1)
	p.SetWaitTimeout(10 * time.Second)
	h := NewHandler(Config{Pool: p, Upstream: up, MaxRotate: 3})

	const N = 20
	var wg sync.WaitGroup
	codes := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, chatReq())
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()

	bad := 0
	for _, c := range codes {
		if c != http.StatusOK {
			bad++
		}
	}
	if bad != 0 {
		t.Fatalf("%d/%d 请求未成功（期望全部 200，名额争夺不应翻成 503）：%v", bad, N, codes)
	}
	if calls != N {
		t.Fatalf("upstream calls=%d want %d", calls, N)
	}
	if got := p.SaturatedCountFor("", "glm-5.2"); got != 0 {
		t.Fatalf("in-flight leaked after all requests done: saturated=%d want 0", got)
	}
}
