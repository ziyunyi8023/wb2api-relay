package upstream

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureRT 记录下游真正收到的请求，供 gzip 包装层断言。
type captureRT struct{ got *http.Request }

func (c *captureRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.got = r
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
}

func TestGzipDisabledByDefault(t *testing.T) {
	SetGzipMinBytes(0)
	t.Cleanup(func() { SetGzipMinBytes(0) })
	cap := &captureRT{}
	rt := wrapGzip(cap)
	body := strings.Repeat("x", 4096)
	req, _ := http.NewRequest("POST", "https://example.invalid/x", strings.NewReader(body))
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if enc := cap.got.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("默认关闭却压了: %q", enc)
	}
}

func TestGzipCompressesLargeBody(t *testing.T) {
	SetGzipMinBytes(1024)
	t.Cleanup(func() { SetGzipMinBytes(0) })
	body := strings.Repeat("abcdefgh", 512) // 4096 B，可压
	cap := &captureRT{}
	rt := wrapGzip(cap)
	req, _ := http.NewRequest("POST", "https://example.invalid/x", strings.NewReader(body))
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	got := cap.got
	if got.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("未压: enc=%q", got.Header.Get("Content-Encoding"))
	}
	raw, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("不是合法 gzip: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != body {
		t.Fatalf("解压后与原文不一致: %d vs %d", len(plain), len(body))
	}
	if got.ContentLength != int64(len(raw)) {
		t.Fatalf("ContentLength=%d want %d", got.ContentLength, len(raw))
	}

}

func TestGzipSkipsSmallBody(t *testing.T) {
	SetGzipMinBytes(4096)
	t.Cleanup(func() { SetGzipMinBytes(0) })
	cap := &captureRT{}
	rt := wrapGzip(cap)
	req, _ := http.NewRequest("POST", "https://example.invalid/x", strings.NewReader("hi"))
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if enc := cap.got.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("小包不该压: %q", enc)
	}
}
