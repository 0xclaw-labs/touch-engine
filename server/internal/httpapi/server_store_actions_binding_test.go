package httpapi

// server_store_actions_binding_test.go: 中央集成验收(2026-11-10)。
// HUI-4584 actions PUT 写入侧已切 HUI-3553 BindActionContent(权威绑定索引):
//   - PUT 建 binding=pack:<id> → 绑定表有行且指向当前采用版本;
//   - PUT 换包 → 重绑新版本(同 (tenant,action) 行覆盖);
//   - PUT 清空 binding → 权威行删除 + 版本事件流审计(superseded);
//   - 未知 pack → 400 且行上 content_binding_ref 还原为本轮前的值;
//   - 未采用 pack → 400 not_distributable 且行值还原;
//   - 整行被删除的已绑动作 → 权威索引跟随清理。
//
// 行值(pack:<id>)是过渡读取副本;权威在 store_action_content_bindings。
// 会让本测试失败的生产改动:PUT 只写行值不落绑定表、绑定失败不回滚行值、
// 或解绑不删权威行不落审计。

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func (f *publicContentFixture) putActions(t *testing.T, tenantID, storeID, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("PUT", f.ts.URL+"/api/v1/stores/"+storeID+"/actions", strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", "sess-owner-a")
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("put actions: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func actionItem(actionID, packRef, label string) string {
	id := ""
	if actionID != "" {
		id = `"action_id":"` + actionID + `",`
	}
	binding := ""
	if packRef != "" {
		binding = `,"content_binding_ref":"` + packRef + `"`
	}
	return `{` + id + `"catalog_code":"A17","label":"` + label + `","target_ref":"https://shop.example.com/wifi","order":1,"enabled":true` + binding + `}`
}

func actionsBody(rev int, items ...string) string {
	return `{"expected_revision":` + itoa(rev) + `,"items":[` + strings.Join(items, ",") + `]}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestActionsPutWritesAuthoritativeBinding(t *testing.T) {
	f := newPublicContentFixture(t, true)
	p1, v1, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包一", true)

	status, out := f.putActions(t, f.tenA, f.storeA, actionsBody(0, actionItem("", "pack:"+p1, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put = %d (%v)", status, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("actions = %v", out)
	}
	actionID, _ := items[0].(map[string]any)["action_id"].(string)
	if actionID == "" {
		t.Fatalf("action_id missing: %v", items[0])
	}

	// 权威绑定表:有行且指向当前采用版本。
	b, err := f.s.St.GetActionContentBinding(f.tenA, actionID)
	if err != nil || b.PackID != p1 || b.LastVersionID != v1 {
		t.Fatalf("binding row = (%v, %+v); want pack %s version %s", err, b, p1, v1)
	}
	// 行上过渡副本仍按现契约存 pack:<id>。
	rows, _, _ := f.s.St.ListStoreActions(f.tenA, f.storeA)
	if rows[0].ContentBindingRef != "pack:"+p1 {
		t.Fatalf("row copy = %q, want pack:%s", rows[0].ContentBindingRef, p1)
	}
}

func TestActionsPutRebindsSwitchedPack(t *testing.T) {
	f := newPublicContentFixture(t, true)
	p1, _, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包一", true)
	p2, v2, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包二", true)

	status, out := f.putActions(t, f.tenA, f.storeA, actionsBody(0, actionItem("", "pack:"+p1, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 1 = %d (%v)", status, out)
	}
	items, _ := out["items"].([]any)
	actionID, _ := items[0].(map[string]any)["action_id"].(string)

	// 换包:同一动作重绑到包二的当前采用版本。
	status, out = f.putActions(t, f.tenA, f.storeA, actionsBody(1, actionItem(actionID, "pack:"+p2, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 2 = %d (%v)", status, out)
	}
	b, err := f.s.St.GetActionContentBinding(f.tenA, actionID)
	if err != nil || b.PackID != p2 || b.LastVersionID != v2 {
		t.Fatalf("rebinding = (%v, %+v); want pack %s version %s", err, b, p2, v2)
	}
	// 同 (tenant,action) 仍只有一行(重绑覆盖,不堆行)。
	if _, err := f.s.St.GetActionContentBinding(f.tenA, actionID); err != nil {
		t.Fatalf("binding row vanished: %v", err)
	}
	rows, _, _ := f.s.St.ListStoreActions(f.tenA, f.storeA)
	if len(rows) != 1 || rows[0].ContentBindingRef != "pack:"+p2 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestActionsPutClearBindingDeletesAuthoritativeRow(t *testing.T) {
	f := newPublicContentFixture(t, true)
	p1, v1, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包一", true)

	status, out := f.putActions(t, f.tenA, f.storeA, actionsBody(0, actionItem("", "pack:"+p1, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 1 = %d (%v)", status, out)
	}
	items, _ := out["items"].([]any)
	actionID, _ := items[0].(map[string]any)["action_id"].(string)

	// 清空:权威行删除 + 审计。
	status, out = f.putActions(t, f.tenA, f.storeA, actionsBody(1, actionItem(actionID, "", "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 2 = %d (%v)", status, out)
	}
	if _, err := f.s.St.GetActionContentBinding(f.tenA, actionID); err != store.ErrNotFound {
		t.Fatalf("binding after clear = %v; want ErrNotFound", err)
	}
	events, err := f.s.St.ListStoreContentPackVersionEvents(f.tenA, v1)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "superseded" && strings.Contains(e.Note, "unbind") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unbind audit event missing: %+v", events)
	}
	// 幂等解绑:再清一次不报错,行值保持空。
	status, _ = f.putActions(t, f.tenA, f.storeA, actionsBody(2, actionItem(actionID, "", "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("idempotent clear = %d", status)
	}
	if _, err := f.s.St.GetActionContentBinding(f.tenA, actionID); err != store.ErrNotFound {
		t.Fatalf("binding after re-clear = %v", err)
	}
}

func TestActionsPutUnknownPackRejectsAndRestoresRows(t *testing.T) {
	f := newPublicContentFixture(t, true)
	p1, v1, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包一", true)

	status, out := f.putActions(t, f.tenA, f.storeA, actionsBody(0, actionItem("", "pack:"+p1, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 1 = %d (%v)", status, out)
	}
	items, _ := out["items"].([]any)
	actionID, _ := items[0].(map[string]any)["action_id"].(string)

	// 未知 pack:整个 PUT 400,行上 content_binding_ref 还原为本轮前的值。
	status, body := f.putActions(t, f.tenA, f.storeA,
		actionsBody(1, actionItem(actionID, "pack:no_such_pack", "改名了")))
	if status != http.StatusBadRequest || body["error"] != "bad_content_binding_ref" {
		t.Fatalf("unknown pack = %d %v; want 400 bad_content_binding_ref", status, body)
	}
	rows, _, _ := f.s.St.ListStoreActions(f.tenA, f.storeA)
	if rows[0].ContentBindingRef != "pack:"+p1 {
		t.Fatalf("row binding not restored: %q", rows[0].ContentBindingRef)
	}
	// 权威绑定行未受本轮失败影响。
	b, err := f.s.St.GetActionContentBinding(f.tenA, actionID)
	if err != nil || b.PackID != p1 || b.LastVersionID != v1 {
		t.Fatalf("binding row disturbed: (%v, %+v)", err, b)
	}
}

func TestActionsPutUnadoptedPackRejected(t *testing.T) {
	f := newPublicContentFixture(t, true)
	p1, _, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包一", true)
	p3, _, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "候选未采用包", false)

	status, out := f.putActions(t, f.tenA, f.storeA, actionsBody(0, actionItem("", "pack:"+p1, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 1 = %d (%v)", status, out)
	}
	items, _ := out["items"].([]any)
	actionID, _ := items[0].(map[string]any)["action_id"].(string)

	// 未采用的包建不了绑定:400 not_distributable,行值还原。
	status, body := f.putActions(t, f.tenA, f.storeA,
		actionsBody(1, actionItem(actionID, "pack:"+p3, "门店 Wi-Fi")))
	if status != http.StatusBadRequest || body["error"] != "not_distributable" {
		t.Fatalf("unadopted pack = %d %v; want 400 not_distributable", status, body)
	}
	rows, _, _ := f.s.St.ListStoreActions(f.tenA, f.storeA)
	if rows[0].ContentBindingRef != "pack:"+p1 {
		t.Fatalf("row binding not restored: %q", rows[0].ContentBindingRef)
	}
}

func TestActionsPutRemovingBoundActionClearsBinding(t *testing.T) {
	f := newPublicContentFixture(t, true)
	p1, _, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "开业包一", true)

	status, out := f.putActions(t, f.tenA, f.storeA, actionsBody(0, actionItem("", "pack:"+p1, "门店 Wi-Fi")))
	if status != http.StatusOK {
		t.Fatalf("put 1 = %d (%v)", status, out)
	}
	items, _ := out["items"].([]any)
	actionID, _ := items[0].(map[string]any)["action_id"].(string)

	// 整行删除:权威索引跟随清理(不留悬置绑定,B05 公共读面随之 404 语义)。
	status, _ = f.putActions(t, f.tenA, f.storeA, actionsBody(1))
	if status != http.StatusOK {
		t.Fatalf("put empty = %d", status)
	}
	if _, err := f.s.St.GetActionContentBinding(f.tenA, actionID); err != store.ErrNotFound {
		t.Fatalf("binding of removed action = %v; want ErrNotFound", err)
	}
}
