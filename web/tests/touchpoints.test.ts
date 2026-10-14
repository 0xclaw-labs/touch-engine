import { describe, expect, it } from "vitest";
import {
  campaignStateList,
  entryStateFace,
  featureEnabled,
  isStage,
  isValidEntryCode,
  nextStage,
  rebindImpactFace,
  sharedStopFace,
  stageFace,
  stageList,
  touchEntryFace,
  touchEntryRedirect,
} from "../src/lib/touchpoints";

describe("FEATURE 开关判定(与 Go isTruthy 同纪律:1/true/yes/on,默认 off)", () => {
  it("1/true/yes/on(任意大小写)为开", () => {
    for (const v of ["1", "true", "TRUE", "yes", "Yes", "on", "ON", " on "]) {
      expect(featureEnabled(v), v).toBe(true);
    }
  });

  it("空/缺失/0/false/no/off/乱值为关", () => {
    for (const v of ["", undefined, null, "0", "false", "no", "off", "enabled", " yes please"]) {
      expect(featureEnabled(v), String(v)).toBe(false);
    }
  });
});

const OK_CODE = "A2C4E6G8J0K2"; // Crockford base32,12 位,与 Go NewShortcode 同源
const BAD_CODES = ["", "short", "AAAAAAAAAAAAA", OK_CODE + "M", "alcdefgh2kmn", OK_CODE.toLowerCase(), "AAAAAAAAAILOU"];

describe("入口码形状闸(与 Go 端 ValidShortcode 同一纪律)", () => {
  it("12 位 Crockford base32 通过", () => {
    expect(isValidEntryCode(OK_CODE)).toBe(true);
    expect(isValidEntryCode("0123456789AB")).toBe(true);
  });

  it("长度不对/带 ILOU/小写/超长一律拒绝", () => {
    for (const bad of BAD_CODES) {
      expect(isValidEntryCode(bad), bad).toBe(false);
    }
  });
});

describe("/t 稳定入口解析分支(白名单,绝不盲跳)", () => {
  it("携带合法 code 的活动态响应才重定向到 /c/{code}", () => {
    for (const state of campaignStateList) {
      expect(touchEntryRedirect({ state, code: OK_CODE })).toBe(`/c/${OK_CODE}`);
    }
  });

  it("别名级停用/不存在/未知状态不跳转", () => {
    for (const state of ["touchpoint_disabled", "not_found", "internal_error", "", undefined, 42]) {
      expect(touchEntryRedirect({ state, code: OK_CODE }), String(state)).toBeNull();
    }
  });

  it("code 缺失或形状非法不跳转(防注入式跳转)", () => {
    for (const code of [undefined, "", "javascript:alert(1)", "/c/evil", BAD_CODES[2]]) {
      expect(touchEntryRedirect({ state: "available", code }), String(code)).toBeNull();
    }
  });

  it("非对象载荷一律不跳转", () => {
    expect(touchEntryRedirect(undefined)).toBeNull();
    expect(touchEntryRedirect(null)).toBeNull();
    expect(touchEntryRedirect("available")).toBeNull();
  });
});

describe("/t 404 文案:诚实、不冒充、不泄漏后台细节", () => {
  it("not_found / touchpoint_disabled / 未知各给一句顾客可读文案", () => {
    expect(touchEntryFace("not_found")).toContain("不存在");
    expect(touchEntryFace("touchpoint_disabled")).toContain("停用");
    expect(touchEntryFace("whatever")).toContain("未知");
    expect(touchEntryFace(undefined)).toContain("未知");
  });

  it("文案不泄漏 uid_hint/角色/租户等后台词汇", () => {
    for (const state of ["not_found", "touchpoint_disabled", "whatever"]) {
      const copy = touchEntryFace(state);
      expect(copy).not.toContain("uid");
      expect(copy).not.toContain("org_owner");
      expect(copy).not.toContain("tenant");
      expect(copy).not.toContain("门店经理");
    }
  });
});

describe("部署五态:单步推进 + 未验证不冒充已部署", () => {
  it("五态顺序与 Go 端 stageOrder 一致", () => {
    expect([...stageList]).toEqual([
      "registered",
      "awaiting_write",
      "written_awaiting_readback",
      "readback_done",
      "device_verified",
    ]);
  });

  it("nextStage 单步前进,终态返回 null,未知返回 null", () => {
    expect(nextStage("registered")).toBe("awaiting_write");
    expect(nextStage("awaiting_write")).toBe("written_awaiting_readback");
    expect(nextStage("written_awaiting_readback")).toBe("readback_done");
    expect(nextStage("readback_done")).toBe("device_verified");
    expect(nextStage("device_verified")).toBeNull();
    expect(nextStage("deployed")).toBeNull();
  });

  it("isStage 拒绝未知态", () => {
    expect(isStage("registered")).toBe(true);
    expect(isStage("deployed")).toBe(false);
    expect(isStage(undefined)).toBe(false);
  });

  it("只有 device_verified 才出现已验证语气;其余态绝不冒充已部署/已上线", () => {
    for (const stage of stageList) {
      const face = stageFace(stage);
      if (stage === "device_verified") {
        expect(face).toContain("已验证");
      } else {
        expect(face).not.toContain("已验证");
        expect(face).not.toContain("已部署");
        expect(face).not.toContain("已上线");
      }
    }
    expect(stageFace("not_a_stage")).toContain("未知");
  });
});

describe("检查态文案:目标活动状态如实呈现", () => {
  it("六种活动态各有一句可读文案", () => {
    expect(entryStateFace("available")).toContain("进入活动");
    expect(entryStateFace("not_started")).toContain("未开始");
    expect(entryStateFace("paused")).toContain("暂停");
    expect(entryStateFace("expired")).toContain("过期");
    expect(entryStateFace("draft")).toContain("草稿");
    expect(entryStateFace("ended")).toContain("结束");
  });

  it("别名停用/不存在/未知不冒充可用", () => {
    expect(entryStateFace("touchpoint_disabled")).toContain("停用");
    expect(entryStateFace("not_found")).toContain("不存在");
    expect(entryStateFace("surprise")).toContain("未知");
    expect(entryStateFace("surprise")).not.toContain("进入活动");
  });
});

describe("换绑影响文案(3546 已开始参与数只读呈现)", () => {
  it("带出参与数并说明不动已承诺参与", () => {
    const face = rebindImpactFace(2);
    expect(face).toContain("2");
    expect(face).toContain("不受影响");
  });

  it("零参与明确说明没有受影响顾客", () => {
    expect(rebindImpactFace(0)).toContain("没有");
  });

  it("负数按 0 处理,不出现负数文案", () => {
    expect(rebindImpactFace(-3)).not.toContain("-3");
    expect(rebindImpactFace(-3)).toContain("没有");
  });
});

describe("旧路径共享停用确认文案(传播影响必须先说清)", () => {
  it("共享 N 张卡要写明一起停掉,且提醒改用稳定入口", () => {
    const face = sharedStopFace(4);
    expect(face).toContain("4");
    expect(face).toContain("共享");
    expect(face).toContain("一起");
    expect(face).toContain("稳定入口");
  });

  it("单卡(1 张)仍如实说明", () => {
    expect(sharedStopFace(1)).toContain("1");
  });
});
