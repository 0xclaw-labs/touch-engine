import { describe, expect, it } from "vitest";
import {
  bindingStaleFace,
  deliveryEventFace,
  deliveryNextStep,
  deliveryStatusFace,
  deliveryStatusList,
  isBindingStale,
  isDeliveryStatus,
  isReplaceReason,
  readbackFace,
  replaceReasonFace,
  replaceReasonList,
} from "../src/lib/touchpoint-deliveries";

describe("交付状态白名单与单步推进(与 Go delivery 包同源)", () => {
  it("七步白名单逐值可判定", () => {
    for (const s of deliveryStatusList) {
      expect(isDeliveryStatus(s), s).toBe(true);
    }
    expect(isDeliveryStatus("superseded")).toBe(false); // 封存态不在推进词表
    expect(isDeliveryStatus("teleported")).toBe(false);
    expect(isDeliveryStatus(42)).toBe(false);
  });

  it("严格线性:每态只到紧邻下一步;终态 null", () => {
    expect(deliveryNextStep("awaiting_dispatch")).toBe("dispatched");
    expect(deliveryNextStep("dispatched")).toBe("bound");
    expect(deliveryNextStep("bound")).toBe("written");
    expect(deliveryNextStep("written")).toBe("readback");
    expect(deliveryNextStep("readback")).toBe("accepted");
    expect(deliveryNextStep("accepted")).toBe("delivered");
    expect(deliveryNextStep("delivered")).toBeNull();
    expect(deliveryNextStep("superseded")).toBeNull();
    expect(deliveryNextStep("nonsense")).toBeNull();
  });

  it("状态文案:未到交付绝不冒充已交付/已验证", () => {
    expect(deliveryStatusFace("awaiting_dispatch")).not.toContain("已交付");
    expect(deliveryStatusFace("written")).toContain("等待回读");
    expect(deliveryStatusFace("readback")).toContain("还没现场验收");
    expect(deliveryStatusFace("accepted")).not.toContain("已交付");
    expect(deliveryStatusFace("delivered")).toContain("已交付");
    expect(deliveryStatusFace("superseded")).toContain("补换");
    expect(deliveryStatusFace("mystery")).toContain("未知");
  });

  it("事件词表文案含七步+补换,未知不冒充", () => {
    for (const ev of ["dispatched", "bound", "written", "readback", "accepted", "delivered", "replaced"]) {
      expect(deliveryEventFace(ev)).not.toContain("未知");
    }
    expect(deliveryEventFace("huh")).toContain("未知");
  });
});

describe("绑定版本 stale(换绑后不得沿用旧「已验证」标识)", () => {
  it("bound/verified 任一非零且 ≠ 当前 → stale", () => {
    expect(isBindingStale({ current: 2, bound: 1, verified: 1 })).toBe(true);
    expect(isBindingStale({ current: 2, bound: 2, verified: 1 })).toBe(true);
    expect(isBindingStale({ current: 1, bound: 1, verified: 1 })).toBe(false);
    expect(isBindingStale({ current: 1, bound: 0, verified: 0 })).toBe(false);
    expect(isBindingStale({ current: undefined })).toBe(false);
  });

  it("stale 文案明说「不适用于当前绑定,不能当作已验证」", () => {
    const stale = bindingStaleFace({ current: 3, bound: 1, verified: 1 });
    expect(stale).toContain("不适用于当前绑定");
    expect(stale).toContain("不能当作已验证");
    const fresh = bindingStaleFace({ current: 1, bound: 1, verified: 1 });
    expect(fresh).not.toContain("不能当作已验证");
  });
});

describe("回读一致性文案(错误域名/错误码不得当已验证)", () => {
  it("一致 = 明说相符", () => {
    expect(readbackFace(true, "ok")).toContain("相符");
  });

  it("不一致按原因给恢复动作,绝不出现「已验证」", () => {
    const host = readbackFace(false, "host_mismatch");
    expect(host).toContain("域名不对");
    const code = readbackFace(false, "code_mismatch");
    expect(code).toContain("码不对");
    const empty = readbackFace(false, "empty_read");
    expect(empty).toContain("为空");
    const fallback = readbackFace(false, "whatever");
    expect(fallback).toContain("重新写卡");
    for (const text of [host, code, empty, fallback]) {
      expect(text).not.toContain("已验证");
    }
  });

  it("没有记录就如实说没有", () => {
    expect(readbackFace(undefined, undefined)).toContain("还没有");
    expect(readbackFace(null, null)).toContain("还没有");
  });
});

describe("补换原因二值(可回收保留入口/遗失撤销入口)", () => {
  it("白名单与文案区分两分支", () => {
    for (const r of replaceReasonList) {
      expect(isReplaceReason(r)).toBe(true);
    }
    expect(isReplaceReason("stolen_shiny")).toBe(false);
    expect(replaceReasonFace("damaged_recoverable")).toContain("同一个入口");
    expect(replaceReasonFace("lost_uncontrolled")).toContain("停用");
    expect(replaceReasonFace("huh")).toContain("未知");
  });
});
