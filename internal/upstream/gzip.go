package upstream

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"sync/atomic"
)

// 出站请求体 gzip 压缩（对齐官方客户端形态，见生态里公开的封号复盘：官方 IDE 的
// body 是 gzip，旧 bridge 未压缩，是「请求画像差异」的一维）。
//
// 为什么用 RoundTripper 而不是在 body 构造处压：chat / refresh / checkin / balance
// 各有自己的构造函数，逐处改会漂移；包一层传输层可以一次覆盖全部出站请求，且
// 天然拿到最终 ContentLength（重写 body 之后 body 里的 model 等字段已定型）。
//
// 默认 0 = 关闭（零回归）。上游未验证接受 gzip 之前不擅自打开：真出现 400/415，
// 代价是全部请求失败，而不是慢一点。
var gzipMinBytes atomic.Int64

// SetGzipMinBytes 设置出站请求体 gzip 压缩阈值：ContentLength >= n 才压。
// <=0 = 关闭压缩（默认）。必须在 upstream.New() 之前调用，New 会据此装配传输层。
func SetGzipMinBytes(n int) {
	gzipMinBytes.Store(int64(n))
}

// GzipMinBytes 报告当前阈值（<=0 表示关闭）。
func GzipMinBytes() int { return int(gzipMinBytes.Load()) }

// gzipRoundTripper 在请求真正发出前把大 body 换成 gzip。
//
// 保守边界，任何一条不满足就原样透传：
//   - 阈值 <= 0（未启用）
//   - 没有 body，或 ContentLength 未知/为负（流式上传不能整体读进内存）
//   - 已经带了 Content-Encoding（别覆盖别人的编码）
//   - GET/HEAD（理论上无 body，出现也不该压）
type gzipRoundTripper struct{ next http.RoundTripper }

func (t *gzipRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	min := gzipMinBytes.Load()
	if min <= 0 || req.Body == nil || req.ContentLength < min {
		return t.next.RoundTrip(req)
	}
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return t.next.RoundTrip(req)
	}
	if req.Header.Get("Content-Encoding") != "" {
		return t.next.RoundTrip(req)
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	// 克隆一份再改：RoundTripper 不应改写调用方的 *Request（文档要求）。
	r2 := req.Clone(req.Context())
	r2.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
	r2.ContentLength = int64(buf.Len())
	r2.Header.Set("Content-Encoding", "gzip")
	r2.Header.Set("Content-Length", strconvItoa(buf.Len()))
	return t.next.RoundTrip(r2)
}

// strconvItoa 避免为一处调用引入 strconv 到本文件（保持 import 面最小）。
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// wrapGzip 按当前阈值包装传输层；阈值为 0 时原样返回（零开销、零行为变化）。
func wrapGzip(rt http.RoundTripper) http.RoundTripper {
	if gzipMinBytes.Load() <= 0 {
		return rt
	}
	return &gzipRoundTripper{next: rt}
}
