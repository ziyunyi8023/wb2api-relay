package server

import (
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// TestRotateNeedsBackoff 账号停车场类错误换号零等待；WAF/网络类保留退避。
func TestRotateNeedsBackoff(t *testing.T) {
	skip := []upstream.ErrKind{
		upstream.ErrSoftRate,
		upstream.ErrHardCredit,
		upstream.ErrModelBlocked,
	}
	keep := []upstream.ErrKind{
		upstream.ErrWafBlock,
		upstream.ErrClient,
		upstream.ErrBadParams,
		upstream.ErrNotFound,
		upstream.ErrContentBlocked,
		upstream.ErrNone,
	}
	for _, k := range skip {
		if rotateNeedsBackoff(k) {
			t.Fatalf("kind=%v should skip backoff", k)
		}
	}
	for _, k := range keep {
		if !rotateNeedsBackoff(k) {
			t.Fatalf("kind=%v must keep backoff", k)
		}
	}
}
