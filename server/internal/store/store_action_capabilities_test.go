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

// ---- Task 3: 持久层行为（扫描/租户作用域/受控 upsert+审计/紧急停用） ---------

func actionCapFixture(mut func(*channeladmission.ActionCapability)) channeladmission.ActionCapability {
	caps := channeladmission.EnvCapabilities{
		Navigation: "supported", MediaHandoff: "supported",
		PlatformReceipt: "unknown", RewardEligibility: "unknown",
	}
	envs := make(map[string]channeladmission.EnvCapabilities, 6)
	for _, e := range channeladmission.Environments {
		envs[e] = caps
	}
	r := channeladmission.ActionCapability{
		ID: "cap_t_x_h5_share", CatalogCode: "A99", Platform: "test",
		Method: "h5_share", Scenario: "store_promo", ActionKind: "publish",
		Label: "测试动作", AppBinding: "none", TenantScope: "*", Status: "admitted",
		Environments:   envs,
		FieldSupport:   map[string]string{"media": "supported"},
		RequiredInputs: []string{"content_pack_version"},
		EvidenceGrade:  "official_doc", OfficialSources: []string{"https://example.test"},
		CheckedAt: "2026-11-09", ValidUntil: "2027-02-07",
		Missing: []string{"缺项"}, ReasonCode: "admitted", ReasonText: "说明", SeedVersion: 1,
	}
	if mut != nil {
		mut(&r)
	}
	return r
}

func countAudit(t *testing.T, s *Store, code string) int {
	t.Helper()
	n, err := s.CountActionCapabilityAudit(code)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

// TestActionCapScanRoundTrip: JSON 列往返一致;坏 environments JSON 扫描报错
// (fail-closed,不让脏行静默通过)。
func TestActionCapScanRoundTrip(t *testing.T) {
	_, s := openMigratedDB(t)
	r := actionCapFixture(func(x *channeladmission.ActionCapability) { x.TenantScope = "t_rt" })
	created, _, err := s.UpsertActionCapability(r, ControlAudit{Actor: "ops", Reason: "初始", Action: "create"})
	if err != nil || !created {
		t.Fatalf("upsert: created=%v err=%v", created, err)
	}
	got, err := s.GetActionCapabilitiesByCode("A99", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty tenant must match nothing: got=%v err=%v", got, err)
	}
	got, err = s.GetActionCapabilitiesByCode("A99", "t_rt")
	if err != nil || len(got) != 1 {
		t.Fatalf("get by code: got=%d err=%v", len(got), err)
	}
	g := got[0]
	if g.CatalogCode != "A99" || g.Platform != "test" || g.Method != "h5_share" ||
		g.Status != "admitted" || g.TenantScope != "t_rt" || len(g.Environments) != 6 ||
		g.Environments["ios_safari"].Navigation != "supported" ||
		len(g.RequiredInputs) != 1 || g.FieldSupport["media"] != "supported" {
		t.Fatalf("round trip mismatch: %+v", g)
	}
	// Corrupt row: scan must fail closed.
	if _, err := s.DB.Exec(`UPDATE channel_action_capabilities SET environments='{"bogus":{}}' WHERE catalog_code='A99'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetActionCapabilitiesByCode("A99", "t_rt"); err == nil {
		t.Fatal("corrupted environments JSON must fail the scan")
	}
}

// TestActionCapTenantScope: '*' 平台真值对每租户可见;定向行只在自己租户可见。
func TestActionCapTenantScope(t *testing.T) {
	_, s := openMigratedDB(t)
	if _, _, err := s.UpsertActionCapability(actionCapFixture(func(x *channeladmission.ActionCapability) {
		x.TenantScope = "t_x"
	}), ControlAudit{Actor: "ops", Reason: "定向行", Action: "create"}); err != nil {
		t.Fatal(err)
	}
	lx, err := s.ListActionCapabilities("t_x")
	if err != nil || len(lx) != 46 { // 45 platform rows + 1 scoped
		t.Fatalf("t_x list = %d err=%v, want 46", len(lx), err)
	}
	ly, err := s.ListActionCapabilities("t_y")
	if err != nil || len(ly) != 45 {
		t.Fatalf("t_y list = %d err=%v, want 45 (never sees t_x's row)", len(ly), err)
	}
	all, err := s.ListActionCapabilitiesAll()
	if err != nil || len(all) != 46 {
		t.Fatalf("all list = %d err=%v, want 46 (ops sees scoped rows)", len(all), err)
	}
}

// TestUpsertAuditIdempotent: hash 未变的重复装载是 no-op(不追加审计);
// 内容变化才 update+审计;create/update 动作分类正确。
func TestUpsertAuditIdempotent(t *testing.T) {
	_, s := openMigratedDB(t)
	r := actionCapFixture(func(x *channeladmission.ActionCapability) { x.TenantScope = "t_u" })
	created, _, err := s.UpsertActionCapability(r, ControlAudit{Actor: "ops-a", Reason: "初值", Action: "create"})
	if err != nil || !created {
		t.Fatalf("first upsert: created=%v err=%v", created, err)
	}
	if n := countAudit(t, s, "A99"); n != 1 {
		t.Fatalf("audit after create = %d, want 1", n)
	}
	created, updated, err := s.UpsertActionCapability(r, ControlAudit{Actor: "ops-a", Reason: "重复", Action: "create"})
	if err != nil || created || updated {
		t.Fatalf("identical re-load: created=%v updated=%v err=%v, want false/false/nil", created, updated, err)
	}
	if n := countAudit(t, s, "A99"); n != 1 {
		t.Fatalf("audit after no-op = %d, want 1 (unchanged re-load writes nothing)", n)
	}
	r.Status = "unknown"
	r.ReasonCode = "pending_external"
	created, updated, err = s.UpsertActionCapability(r, ControlAudit{Actor: "ops-b", Reason: "资格复核", Action: "update"})
	if err != nil || created {
		t.Fatalf("changed re-load: created=%v err=%v, want false (update path)", created, err)
	}
	if n := countAudit(t, s, "A99"); n != 2 {
		t.Fatalf("audit after update = %d, want 2", n)
	}
	got, _ := s.GetActionCapabilitiesByCode("A99", "t_u")
	if got[0].Status != "unknown" || got[0].ReasonCode != "pending_external" {
		t.Fatalf("update not applied: %+v", got[0])
	}
	rows, err := s.ListActionCapabilityAudit("A99", 10)
	if err != nil || len(rows) != 2 || rows[0].Actor != "ops-a" || rows[1].Actor != "ops-b" || rows[1].Action != "update" {
		t.Fatalf("audit trail = %+v err=%v", rows, err)
	}
	if rows[0].Reason != "初值" || rows[0].PayloadHash == "" || rows[0].CreatedAt == "" {
		t.Fatalf("audit fields incomplete: %+v", rows[0])
	}
}

// TestEmergencyToggleAudit: 紧急停用=独立否决,写必留审计;未知 id 拒绝且无审计。
func TestEmergencyToggleAudit(t *testing.T) {
	_, s := openMigratedDB(t)
	r := actionCapFixture(func(x *channeladmission.ActionCapability) { x.TenantScope = "t_e" })
	if _, _, err := s.UpsertActionCapability(r, ControlAudit{Actor: "ops", Reason: "初值", Action: "create"}); err != nil {
		t.Fatal(err)
	}
	found, err := s.SetActionCapabilityEmergency(r.ID, true, ControlAudit{Actor: "ops-oncall", Reason: "平台故障停用", Action: "emergency_disable"})
	if err != nil || !found {
		t.Fatalf("emergency disable: found=%v err=%v", found, err)
	}
	got, _ := s.GetActionCapabilitiesByCode("A99", "t_e")
	if !got[0].EmergencyDisabled {
		t.Fatal("emergency_disabled not persisted")
	}
	if n := countAudit(t, s, "A99"); n != 2 {
		t.Fatalf("audit = %d, want 2 (create + emergency_disable)", n)
	}
	rows, _ := s.ListActionCapabilityAudit("A99", 10)
	if rows[1].Action != "emergency_disable" || rows[1].Actor != "ops-oncall" {
		t.Fatalf("disable audit = %+v", rows[1])
	}
	found, err = s.SetActionCapabilityEmergency("cap_nope", true, ControlAudit{Actor: "ops", Reason: "x", Action: "emergency_disable"})
	if found || err == nil {
		t.Fatalf("unknown id: found=%v err=%v, want false/ErrNotFound", found, err)
	}
	if n := countAudit(t, s, "A99"); n != 2 {
		t.Fatalf("failed toggle must not audit: %d", n)
	}
}

// TestSeedHashParity: 迁移种子行的 payload_hash 与 Go 端 ActionCapabilityPayloadHash
// 一致(同 13 规范字段 canonical JSON)——种子行的首次受控重写若内容不变必须
// 是 no-op(不产生 update 审计),哈希口径漂移会让每次启动后的首次写都假更新。
func TestSeedHashParity(t *testing.T) {
	_, s := openMigratedDB(t)
	rows, err := s.ListActionCapabilitiesAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 45 {
		t.Fatalf("rows = %d, want 45", len(rows))
	}
	for _, r := range rows {
		var stored string
		if err := s.DB.QueryRow(`SELECT payload_hash FROM channel_action_capabilities WHERE id=?`, r.ID).Scan(&stored); err != nil {
			t.Fatalf("%s: %v", r.IdentityKey(), err)
		}
		if got := ActionCapabilityPayloadHash(r); got != stored {
			t.Fatalf("%s hash drift: go=%s stored=%s", r.IdentityKey(), got, stored)
		}
	}
	// 不变更的种子行受控重写 = no-op,无审计。
	a01, err := s.GetActionCapabilitiesByCode("A01", "")
	if err != nil || len(a01) == 0 {
		t.Fatalf("A01: %v", err)
	}
	var target channeladmission.ActionCapability
	for _, r := range a01 {
		if r.Method == "h5_share" {
			target = r
		}
	}
	created, updated, err := s.UpsertActionCapability(target, ControlAudit{Actor: "ops", Reason: "复核", Action: "update"})
	if err != nil || created || updated {
		t.Fatalf("unchanged seed rewrite: created=%v updated=%v err=%v, want false/false", created, updated, err)
	}
	if n := countAudit(t, s, "A01"); n != 0 {
		t.Fatalf("audit after unchanged rewrite = %d, want 0", n)
	}
}

// TestGetByCodeBestMatchCollapse: 公共解析路径的同自然键 best-match 收敛——
// (platform,method,scenario) 相同时定向行压过 '*' 行,绝不同响应双路由
// (与 v1 GetChannelAdmission 单行 best-match 同语义)。
func TestGetByCodeBestMatchCollapse(t *testing.T) {
	_, s := openMigratedDB(t)
	scoped := seededA02Row(t, s)
	scoped.ID = ""
	scoped.TenantScope = "t_best"
	scoped.Status = "unknown"
	scoped.ReasonCode = "pending_external"
	if _, _, err := s.UpsertActionCapability(scoped, ControlAudit{Actor: "ops", Reason: "定向复核", Action: "update"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetActionCapabilitiesByCode("A02", "t_best")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range rows {
		key := r.Platform + "|" + r.Method + "|" + r.Scenario
		seen[key]++
		if key == "xiaohongshu|js_media_handoff|store_promo" && r.TenantScope != "t_best" {
			t.Fatalf("best-match broken: kept %s row, want t_best", r.TenantScope)
		}
	}
	if n := seen["xiaohongshu|js_media_handoff|store_promo"]; n != 1 {
		t.Fatalf("duplicated natural key in resolution path: %d rows, want 1 (best-match 收敛)", n)
	}
	// 无定向行的租户仍见 '*'。
	rowsOther, err := s.GetActionCapabilitiesByCode("A02", "t_other")
	if err != nil || len(rowsOther) != 2 {
		t.Fatalf("t_other rows = %d err=%v, want 2 (A02 全部 '*' 行)", len(rowsOther), err)
	}
}

// seededA02Row fetches the seeded A02 js row for mutation.
func seededA02Row(t *testing.T, s *Store) channeladmission.ActionCapability {
	t.Helper()
	rows, err := s.GetActionCapabilitiesByCode("A02", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Method == "js_media_handoff" && r.TenantScope == "*" {
			return r
		}
	}
	t.Fatal("seeded A02 js_media_handoff row not found")
	return channeladmission.ActionCapability{}
}
