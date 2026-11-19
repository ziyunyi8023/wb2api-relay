// FEFO（First-Expire-First-Out）路由：prefer_expiring 开启时按最早到期日的
// 紧迫度给权重，而不是按「谁积分多谁先花」。
//
// 背景：旧口径 credits 比例 ×10 让历史余额最高的号被优先榨干（实测一个
// 3822 总额的号被烧到 230，而同池 2100 总额的号只烧掉 100）——长期积分
// 反而攒着不花。到期临近才是真金白银要烧掉的，所以由紧迫度主导。
package pool

import (
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

// fefoUrgency 的边界与单调性。
func TestFEFOUrgencyOrdering(t *testing.T) {
	now := time.Date(2026, 11, 19, 5, 0, 0, 0, time.Local)
	at := func(days float64) float64 {
		return fefoUrgency(now.Add(time.Duration(days*24*float64(time.Hour))), now)
	}

	if got := fefoUrgency(time.Time{}, now); got != 0 {
		t.Errorf("零到期日 urgency=%v want 0（无到期信息不应参与 FEFO）", got)
	}
	if got := fefoUrgency(now.Add(-time.Hour), now); got != 1 {
		t.Errorf("已过期 urgency=%v want 1（最高紧迫度）", got)
	}

	near, mid, far := at(9), at(29), at(342)
	if !(near > mid && mid > far) {
		t.Fatalf("紧迫度非单调递减：9天=%v 29天=%v 342天=%v", near, mid, far)
	}
	// 关键量级：紧迫度差要能压过余额项（×10 的满量程），否则 FEFO 名存实亡。
	if near-far < 0.5 {
		t.Fatalf("9天(%v) 与 342天(%v) 紧迫度差仅 %v，压不过余额项", near, far, near-far)
	}
	if far > 0.1 {
		t.Errorf("342 天到期 urgency=%v want ≤0.1（长期积分不该显得紧迫）", far)
	}
}

// FEFO 主导权重：余额少但到期近的号必须压过余额多但到期远的号。
func TestWeightPrefersNearestExpiry(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "soon"})
	p.Add(&auth.Auth{UID: "later"})
	now := time.Now()
	// soon：230 分 / 9 天后到期；later：2785 分 / 342 天后到期。
	p.SetCreditsDetailed("soon", 230, 3822, 0, now.AddDate(0, 0, 9), 230)
	p.SetCreditsDetailed("later", 2785, 4146, 0, now.AddDate(0, 0, 342), 2785)

	p.mu.RLock()
	wSoon := p.weightOf(p.byUID["soon"], 2785, now)
	wLater := p.weightOf(p.byUID["later"], 2785, now)
	p.mu.RUnlock()

	if wSoon <= wLater {
		t.Fatalf("FEFO 未生效：soon(230分/9天) w=%v 未超过 later(2785分/342天) w=%v", wSoon, wLater)
	}
}

// 关掉 prefer_expiring 后退回旧口径：积分多的优先（保证开关语义真实可回滚）。
func TestWeightCreditsFirstWhenPreferExpiringOff(t *testing.T) {
	p := New("")
	p.SetPreferExpiring(false)
	p.Add(&auth.Auth{UID: "soon"})
	p.Add(&auth.Auth{UID: "later"})
	now := time.Now()
	p.SetCreditsDetailed("soon", 230, 3822, 0, now.AddDate(0, 0, 9), 230)
	p.SetCreditsDetailed("later", 2785, 4146, 0, now.AddDate(0, 0, 342), 2785)

	p.mu.RLock()
	wSoon := p.weightOf(p.byUID["soon"], 2785, now)
	wLater := p.weightOf(p.byUID["later"], 2785, now)
	p.mu.RUnlock()

	if wLater <= wSoon {
		t.Fatalf("prefer_expiring=false 应回到积分优先：later w=%v 未超过 soon w=%v", wLater, wSoon)
	}
}

// 零余额号不因「到期最近」被抬高权重：选中即 402，白烧一轮轮换。
func TestWeightFEFOSkipsExhaustedAccount(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "empty"})
	p.Add(&auth.Auth{UID: "full"})
	now := time.Now()
	p.SetCreditsDetailed("empty", 0, 3822, 0, now.AddDate(0, 0, 1), 0)
	p.SetCreditsDetailed("full", 500, 2100, 0, now.AddDate(0, 0, 200), 500)

	p.mu.RLock()
	wEmpty := p.weightOf(p.byUID["empty"], 500, now)
	wFull := p.weightOf(p.byUID["full"], 500, now)
	p.mu.RUnlock()

	if wEmpty >= wFull {
		t.Fatalf("零余额号被 FEFO 抬高：empty(0分/1天) w=%v >= full(500分/200天) w=%v", wEmpty, wFull)
	}
}

// 端到端：加权抽签确实偏向快过期的号。
func TestPickFEFODominates(t *testing.T) {
	withNoPickGap(t)
	p := New("")
	p.SetCostExploreInterval(0)
	p.SetCreditFloor(0)
	p.Add(&auth.Auth{UID: "soon"})
	p.Add(&auth.Auth{UID: "later"})
	now := time.Now()
	p.SetCreditsDetailed("soon", 230, 3822, 0, now.AddDate(0, 0, 9), 230)
	p.SetCreditsDetailed("later", 2785, 4146, 0, now.AddDate(0, 0, 342), 2785)

	soonHits := 0
	const rounds = 300
	for i := 0; i < rounds; i++ {
		a := p.PickExcludingForRealm(nil, "", "")
		if a == nil {
			t.Fatalf("第 %d 次选号返回 nil", i)
		}
		if a.UID == "soon" {
			soonHits++
		}
	}
	// 理论占比约 60%（闲置补偿会把两者的差距拉近，所以不断言 85%）。
	if soonHits <= rounds/2 {
		t.Fatalf("300 次仅命中快过期号 %d 次（不超过一半），FEFO 未主导抽签", soonHits)
	}
}
