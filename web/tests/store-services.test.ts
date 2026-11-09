import { describe, expect, it } from "vitest";
import {
  buildServicesPutBody,
  canSaveServices,
  isOfficialWecomURL,
  normalizeServiceItems,
  saveServicesFailure,
  serviceSummary,
  servicesSaveError,
  wecomCallbackBadge,
  type ServiceItem,
} from "../src/lib/store-services";

// HUI-4589 商家服务配置纯逻辑：乐观锁/冲突剧本/kind 校验前端先拦/回调资质诚实徽章。

function wifi(partial: Partial<ServiceItem> = {}): ServiceItem {
  return {
    service_id: "",
    catalog_code: "A17",
    kind: "wifi",
    variant: "",
    label: "门店 Wi-Fi",
    config: { ssid: "Painuo-Guest", password: "passw0rd123", encryption: "wpa2" },
    order: 1,
    enabled: true,
    ...partial,
  };
}

describe("normalizeServiceItems 行级阻塞（与服务端校验同口径）", () => {
  it("启用 Wi-Fi 缺 SSID/短密码被拦；open 网络有密码被拦", () => {
    const [noSsid] = normalizeServiceItems([wifi({ config: { ssid: "", password: "passw0rd123", encryption: "wpa2" } })]);
    expect(noSsid.blocker).toContain("Wi-Fi 名称");
    const [short] = normalizeServiceItems([wifi({ config: { ssid: "S", password: "1234567", encryption: "wpa2" } })]);
    expect(short.blocker).toContain("8-64 位");
    const [openWithPassword] = normalizeServiceItems([wifi({ config: { ssid: "S", password: "passw0rd123", encryption: "open" } })]);
    expect(openWithPassword.blocker).toContain("开放网络不留密码");
    const [ok] = normalizeServiceItems([wifi()]);
    expect(ok.blocker).toBe("");
  });

  it("企微必须官方域名；个人微信二者必居其一；微信号形状校验", () => {
    const [evil] = normalizeServiceItems([{ ...wifi(), kind: "contact", variant: "wecom", config: { url: "https://evil.example.com/ca" } }]);
    expect(evil.blocker).toContain("work.weixin.qq.com");
    const [official] = normalizeServiceItems([{ ...wifi(), kind: "contact", variant: "wecom", config: { url: "https://work.weixin.qq.com/ca/abc" } }]);
    expect(official.blocker).toBe("");
    const [neither] = normalizeServiceItems([{ ...wifi(), kind: "contact", variant: "personal", config: {} }]);
    expect(neither.blocker).toContain("至少填一项");
    const [badId] = normalizeServiceItems([{ ...wifi(), kind: "contact", variant: "personal", config: { wechat_id: "1abc" } }]);
    expect(badId.blocker).toContain("字母开头");
    expect(isOfficialWecomURL("https://work.weixin.qq.com/ca/x?source=1")).toBe(true);
    expect(isOfficialWecomURL("https://fake-work.weixin.qq.com.evil.com/ca")).toBe(false);
  });

  it("社群至少一项；未选类型被拦", () => {
    const [empty] = normalizeServiceItems([{ ...wifi(), kind: "community", config: {} }]);
    expect(empty.blocker).toContain("至少填一项");
    const [noKind] = normalizeServiceItems([{ ...wifi(), kind: "" }]);
    expect(noKind.blocker).toContain("选择服务类型");
  });
});

describe("buildServicesPutBody：open 网络密码规范化空串", () => {
  it("open 时 password 置空提交", () => {
    const body = buildServicesPutBody([wifi({ config: { ssid: "OpenNet", password: "whatever1", encryption: "open" } })], 3);
    expect(body.expected_revision).toBe(3);
    expect(body.items[0].config.password).toBe("");
    expect(body.items[0].order).toBe(1);
  });
});

describe("保存失败分流与文案", () => {
  it("409 是独立冲突剧本；400 细分文案", () => {
    expect(saveServicesFailure(409, { error: "stale_version" }).kind).toBe("conflict");
    expect(saveServicesFailure(400, { error: "bad_service_config", message: "url 必须是…" }).kind).toBe("error");
    expect(servicesSaveError(400, { error: "bad_service_config", message: "url 必须是…" })).toContain("url 必须是…");
    expect(servicesSaveError(400, { error: "duplicate_service_id" })).toContain("两次");
    expect(servicesSaveError(403, { error: "forbidden" })).toContain("管辖");
  });

  it("保存闸：stale/reloading/脏/阻塞组合", () => {
    expect(canSaveServices({ stale: false, reloading: false, dirty: true, blockers: 0 })).toBe(true);
    expect(canSaveServices({ stale: true, reloading: false, dirty: true, blockers: 0 })).toBe(false);
    expect(canSaveServices({ stale: false, reloading: false, dirty: true, blockers: 1 })).toBe(false);
  });
});

describe("企微回调资质徽章（待资质诚实态）", () => {
  it("无凭据绝不显示已配置", () => {
    expect(wecomCallbackBadge(undefined).tone).toBe("warn");
    expect(wecomCallbackBadge({ state: "pending_qualification" }).text).toContain("待资质");
    expect(wecomCallbackBadge({ state: "configured" }).tone).toBe("ok");
    expect(wecomCallbackBadge({ state: "configured" }).text).toContain("以企业微信官方为准");
  });
});

describe("serviceSummary 列表摘要不泄露完整密码", () => {
  it("Wi-Fi 摘要只给名称与已设置状态", () => {
    const text = serviceSummary(wifi());
    expect(text).toContain("Painuo-Guest");
    expect(text).not.toContain("passw0rd123");
  });
});
