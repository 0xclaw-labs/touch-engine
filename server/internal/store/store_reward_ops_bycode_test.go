package store

// HUI-3562 店员台按券码定位权益(只读查询)。
// 纪律:租户边界强制;未知识码 ErrNotFound(防枚举与防错扫);
// 无码权益(现场赠品/折扣)天然查不到——核销走权益记录入口。

import (
	"errors"
	"testing"
)

func TestGetRewardBenefitByCode(t *testing.T) {
	s, tenA, campA, invA, tenB, campB, invB := seedRewardWorld(t)

	if err := s.insertRawBenefit("ben-1", tenA, campA, invA, "pt-1", "first_publish", "staff_observed_submission", "CODE-AAA1", "issued", "2026-10-14T10:00:00Z"); err != nil {
		t.Fatalf("seed benefit: %v", err)
	}
	if err := s.insertRawBenefit("ben-2", tenA, campA, invA, "pt-2", "first_publish", "staff_observed_submission", "CODE-BBB2", "redeemed", "2026-10-14T11:00:00Z"); err != nil {
		t.Fatalf("seed benefit: %v", err)
	}

	// 命中:租户内按码定位。
	ben, err := s.GetRewardBenefitByCode(tenA, "CODE-AAA1")
	must(t, err)
	if ben.ID != "ben-1" || ben.Status != "issued" {
		t.Fatalf("by-code = %s/%s, want ben-1/issued", ben.ID, ben.Status)
	}

	// 未知识码:ErrNotFound。
	if _, err := s.GetRewardBenefitByCode(tenA, "CODE-NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown code = %v, want ErrNotFound", err)
	}
	// 空码:ErrNotFound(fail-closed,不打全表)。
	if _, err := s.GetRewardBenefitByCode(tenA, "  "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blank code = %v, want ErrNotFound", err)
	}
	// 租户边界:同码不跨租户可见。
	if err := s.insertRawBenefit("ben-b", tenB, campB, invB, "pt-b", "first_publish", "staff_observed_submission", "CODE-AAA1", "issued", "2026-10-14T09:00:00Z"); err != nil {
		t.Fatalf("seed foreign benefit: %v", err)
	}
	ben, err = s.GetRewardBenefitByCode(tenB, "CODE-AAA1")
	must(t, err)
	if ben.ID != "ben-b" {
		t.Fatalf("tenant scoping broken: got %s", ben.ID)
	}
}
