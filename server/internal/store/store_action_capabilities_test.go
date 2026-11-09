package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// openMigratedDB gives a real migrated database (embed migrations apply in
// filename order, so 0060-0063 are part of every fresh store fixture).
func openMigratedDB(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/touch.db")
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d, New(d)
}

// ---- Task 2: migration seed truth (裁定书三钉子 + 全目录覆盖) -------------------

func actionRowCount(t *testing.T, d *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

func listActionRows(t *testing.T, d *sql.DB) []map[string]any {
	t.Helper()
	rows, err := d.Query(`SELECT id,catalog_code,platform,method,scenario,action_kind,label,
		suggested_only,app_binding,tenant_scope,status,environments,field_support,
		required_inputs,evidence_grade,official_sources,checked_at,valid_until,
		emergency_disabled,close_reason,missing,reason_code,reason_text,seed_version,payload_hash
		FROM channel_action_capabilities ORDER BY catalog_code, method`)
	if err != nil {
		t.Fatalf("list action rows: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		m := map[string]any{}
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMigrationSeeds45Rows: A01–A30 全目录覆盖,45 条方法路线,A22–A30 共 11 行
// 建议补充标识(suggested_only=1),A01–A21 必需项不受建议标签污染。
func TestMigrationSeeds45Rows(t *testing.T) {
	d, _ := openMigratedDB(t)
	if n := actionRowCount(t, d, `SELECT COUNT(1) FROM channel_action_capabilities`); n != 45 {
		t.Fatalf("seed rows = %d, want 45", n)
	}
	if n := actionRowCount(t, d, `SELECT COUNT(DISTINCT catalog_code) FROM channel_action_capabilities`); n != 30 {
		t.Fatalf("distinct catalog codes = %d, want 30 (A01–A30)", n)
	}
	if n := actionRowCount(t, d, `SELECT COUNT(1) FROM channel_action_capabilities WHERE suggested_only=1`); n != 11 {
		t.Fatalf("suggested_only rows = %d, want 11 (A22–A30)", n)
	}
	for i := 1; i <= 30; i++ {
		code := "A" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		if n := actionRowCount(t, d, `SELECT COUNT(1) FROM channel_action_capabilities WHERE catalog_code=?`, code); n == 0 {
			t.Fatalf("catalog code %s missing from seeds", code)
		}
	}
}

// TestSeedRulingPins locks the ruling's three pins and the honesty invariants:
//  1. 抖音店宣通用投稿=未准入(拒绝原因明确),含显式手工绕过拒绝行;
//  2. 点评类=navigation only + reward_eligibility=unsupported;
//  3. 微信普通 H5 原生发帖契约未核实保持 unknown(unverified),manual 交接可交付;
//  4. unknown 永不开奖励;六环境键完整且四分三态合法;日期口径一致。
func TestSeedRulingPins(t *testing.T) {
	d, _ := openMigratedDB(t)
	rows := listActionRows(t, d)
	byKey := map[string]map[string]any{}
	for _, r := range rows {
		key := r["catalog_code"].(string) + "|" + r["method"].(string)
		if _, dup := byKey[key]; dup {
			t.Fatalf("duplicate seed key %s", key)
		}
		byKey[key] = r
	}

	pin := func(key, field string, want any) {
		t.Helper()
		r, ok := byKey[key]
		if !ok {
			t.Fatalf("seed row %s missing", key)
		}
		got := r[field]
		if got != want {
			t.Fatalf("%s.%s = %v, want %v", key, field, got, want)
		}
	}

	// Pin 1: douyin store_promo generic submission refused, 手工路线显式拒绝。
	pin("A01|h5_share", "status", "not_admitted")
	pin("A01|h5_share", "reason_code", "not_admitted")
	pin("A01|h5_share", "action_kind", "publish")
	pin("A01|manual_material", "status", "not_admitted")
	pin("A01|manual_material", "reason_code", "not_admitted")

	// Pin 2: reviews are navigation only, reward explicitly unsupported.
	for _, code := range []string{"A08", "A09", "A10", "A11", "A12"} {
		r, ok := byKey[code+"|direct_open"]
		if !ok {
			t.Fatalf("review row %s|direct_open missing", code)
		}
		if r["status"] != "admitted" {
			t.Fatalf("%s status = %v, want admitted (入口只帮找到正确平台门店)", code, r["status"])
		}
		envs := mustEnvs(t, r)
		for e, c := range envs {
			if c.Navigation != "supported" || c.MediaHandoff != "unsupported" || c.RewardEligibility != "unsupported" {
				t.Fatalf("%s/%s four-way = %+v, want navigation only + reward unsupported", code, e, c)
			}
		}
		if rt, _ := r["reason_text"].(string); rt == "" {
			t.Fatalf("%s review row must carry the 评价纪律 human explanation", code)
		}
	}

	// Pin 3: wechat plain-H5 native publish stays unknown/unverified; the
	// manual_material route is the deliverable path (admitted).
	pin("A04|h5_share", "status", "unknown")
	pin("A04|h5_share", "evidence_grade", "unverified")
	pin("A05|h5_share", "status", "unknown")
	pin("A06|h5_share", "evidence_grade", "unverified")
	pin("A04|manual_material", "status", "admitted")
	pin("A05|manual_material", "status", "admitted")
	pin("A06|manual_material", "status", "admitted")

	// A27 纯链接分享: admitted 且无需内容包/媒体授权。
	pin("A27|link_share", "status", "admitted")
	pin("A27|link_share", "method", "link_share")
	var inputs []string
	if err := json.Unmarshal([]byte(byKey["A27|link_share"]["required_inputs"].(string)), &inputs); err != nil {
		t.Fatalf("A27 required_inputs json: %v", err)
	}
	if len(inputs) != 0 {
		t.Fatalf("A27 link_share required_inputs = %v, want [] (合法 link_share 无需内容包/媒体授权)", inputs)
	}
	// 媒体发布路线要求真实采用版本。
	for _, key := range []string{"A02|js_media_handoff", "A03|oauth_server_push", "A04|manual_material"} {
		r, ok := byKey[key]
		if !ok {
			t.Fatalf("seed row %s missing", key)
		}
		var ri []string
		if err := json.Unmarshal([]byte(r["required_inputs"].(string)), &ri); err != nil {
			t.Fatalf("%s required_inputs json: %v", key, err)
		}
		if len(ri) == 0 {
			t.Fatalf("%s media route must require content_pack_version", key)
		}
	}

	// Honesty invariants over every row.
	for key, r := range byKey {
		status, _ := r["status"].(string)
		checked, _ := r["checked_at"].(string)
		valid, _ := r["valid_until"].(string)
		if checked != "2026-11-09" {
			t.Fatalf("%s checked_at = %v, want 2026-11-09 (票面官方检索日)", key, checked)
		}
		if valid != "2027-02-07" {
			t.Fatalf("%s valid_until = %v, want 2027-02-07 (90 天复检窗)", key, valid)
		}
		if r["tenant_scope"] != "*" {
			t.Fatalf("%s tenant_scope = %v, want * (种子=平台级初值)", key, r["tenant_scope"])
		}
		if rt, _ := r["reason_text"].(string); rt == "" {
			t.Fatalf("%s reason_text empty (返回结构必须含 human 说明)", key)
		}
		if src, _ := r["official_sources"].(string); src == "" || src == "[]" {
			t.Fatalf("%s official_sources empty (证据引用必填)", key)
		}
		envs := mustEnvs(t, r)
		if len(envs) != 6 {
			t.Fatalf("%s environments keys = %d, want 6 (六类环境按环境分别给值)", key, len(envs))
		}
		for e, c := range envs {
			for name, v := range map[string]string{
				"navigation": c.Navigation, "media_handoff": c.MediaHandoff,
				"platform_receipt": c.PlatformReceipt, "reward_eligibility": c.RewardEligibility,
			} {
				if v != "supported" && v != "unsupported" && v != "unknown" {
					t.Fatalf("%s/%s %s = %q illegal", key, e, name, v)
				}
			}
			if status != "admitted" && c.RewardEligibility == "supported" {
				t.Fatalf("%s/%s: status=%s 但 reward_eligibility=supported (unknown 永不开奖励)", key, e, status)
			}
		}
	}
}

func mustEnvs(t *testing.T, row map[string]any) map[string]channeladmission.EnvCapabilities {
	t.Helper()
	raw, _ := row["environments"].(string)
	var envs map[string]channeladmission.EnvCapabilities
	if err := json.Unmarshal([]byte(raw), &envs); err != nil {
		t.Fatalf("environments json: %v", err)
	}
	return envs
}

// TestControlLogEmpty: 受控审计表由迁移建立但种子不经审计(种子=版本化发布初值;
// 只有受控端点写操作留痕)。
func TestControlLogEmpty(t *testing.T) {
	d, _ := openMigratedDB(t)
	if n := actionRowCount(t, d, `SELECT COUNT(1) FROM channel_admission_control_log`); n != 0 {
		t.Fatalf("control log rows = %d, want 0 (种子初值不经审计表)", n)
	}
}
