// drain_test.go 优雅停机回归：换版/重启时在途请求必须被排空，进程不能提前退出。
//
// 场景（2026-11-19 实读代码复现）：原实现把 srv.Shutdown 丢进后台 goroutine 就返回
// main，ListenAndServe 一旦因 Shutdown 返回 ErrServerClosed，进程立即退出——在飞的
// SSE 流被内核切断，客户端表现为 Connection error。本测试用「慢响应在途请求」证明
// serveWithDrain 会等到它结束才返回。
package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeWithDrainWaitsForInflight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // 模拟一个还在跑的 SSE/长响应
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "done")
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flushed := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- serveWithDrain(srv, ln, ctx, func() { flushed <- struct{}{} }, 10*time.Second) }()

	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	<-started

	cancel()
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 flush 回调没有执行（信号路径断了）")
	}

	// 在途请求仍卡着：此时绝不能返回，否则进程退出会切断它。
	select {
	case got := <-done:
		t.Fatalf("在途请求未完成 serveWithDrain 就返回了（排空被跳过），err=%v", got)
	case <-time.After(300 * time.Millisecond):
	}

	close(release) // 放行在途请求
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("serveWithDrain 返回错误: %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("放行在途请求后 serveWithDrain 仍未返回")
	}
}

func TestServeWithDrainReturnsListenError(t *testing.T) {
	// 端口被占用等监听失败要原样返回，不能假装排空成功。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close() // 关掉后 Serve 立刻失败

	srv := &http.Server{Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := serveWithDrain(srv, ln, ctx, nil, time.Second); err == nil {
		t.Fatal("listener 已关闭时 serveWithDrain 应返回错误")
	}
}
