import { describe, expect, it } from "vitest";
import { acceptReturn, EMPTY_RETURN_STATE, type ReturnReceipt, type ReturnState } from "@/lib/mx-ops/return";

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";

function receipt(patch: Partial<ReturnReceipt> = {}): ReturnReceipt {
  return {
    idempotencyKey: "ret_1",
    creativeRef: "creative_1",
    candidateRef: "cpv_1",
    contentDigest: digestA,
    receivedAtUnix: 1780000000,
    source: "product_photo_return",
    ...patch,
  };
}

describe("回流只进候选，显式采用才生效", () => {
  it("首回执进入候选，adopted 不变且不自动采用", () => {
    const result = acceptReturn(EMPTY_RETURN_STATE, receipt());
    expect(result.state.candidates).toHaveLength(1);
    expect(result.state.adoptedRef).toBeNull();
    expect(result.replayed).toBe(false);
    expect(result.conflict).toBe(false);
    expect(result.autoAdopted).toBe(false);
  });

  it("同幂等键同内容 = 重放：不新增候选、不重生成", () => {
    const first = acceptReturn(EMPTY_RETURN_STATE, receipt());
    const second = acceptReturn(first.state, receipt({ receivedAtUnix: 1780009999 }));
    expect(second.replayed).toBe(true);
    expect(second.conflict).toBe(false);
    expect(second.state.candidates).toHaveLength(1);
    expect(second.state.candidates[0]?.candidateRef).toBe("cpv_1");
  });

  it("同幂等键不同内容 = 幂等冲突，状态不变", () => {
    const first = acceptReturn(EMPTY_RETURN_STATE, receipt());
    const second = acceptReturn(first.state, receipt({ contentDigest: digestB, candidateRef: "cpv_2" }));
    expect(second.conflict).toBe(true);
    expect(second.replayed).toBe(false);
    expect(second.state.candidates).toHaveLength(1);
    expect(second.state.candidates[0]?.candidateRef).toBe("cpv_1");
  });

  it("晚到旧稿只追加候选，已启用版本不静默变化", () => {
    const adopted: ReturnState = { candidates: [], adoptedRef: "cpv_live" };
    const result = acceptReturn(adopted, receipt({ idempotencyKey: "ret_late", candidateRef: "cpv_old" }));
    expect(result.state.adoptedRef).toBe("cpv_live");
    expect(result.late).toBe(true);
    expect(result.state.candidates).toHaveLength(1);
    expect(result.autoAdopted).toBe(false);
  });

  it("重复的晚到回执不重复追加", () => {
    const adopted: ReturnState = { candidates: [], adoptedRef: "cpv_live" };
    const first = acceptReturn(adopted, receipt({ idempotencyKey: "ret_late" }));
    const second = acceptReturn(first.state, receipt({ idempotencyKey: "ret_late" }));
    expect(second.replayed).toBe(true);
    expect(second.state.candidates).toHaveLength(1);
  });

  it("digest 规范化：大小写与 sha256: 前缀不造成假冲突", () => {
    const first = acceptReturn(EMPTY_RETURN_STATE, receipt({ contentDigest: digestA }));
    const second = acceptReturn(first.state, receipt({ contentDigest: digestA.toUpperCase() }));
    expect(second.conflict).toBe(false);
    expect(second.replayed).toBe(true);
  });

  it("缺 idempotency key 或 digest 的回执拒绝", () => {
    const result = acceptReturn(EMPTY_RETURN_STATE, receipt({ idempotencyKey: "" }));
    expect(result.rejected).toBe(true);
    expect(result.state.candidates).toHaveLength(0);
    const noDigest = acceptReturn(EMPTY_RETURN_STATE, receipt({ contentDigest: "" }));
    expect(noDigest.rejected).toBe(true);
  });
});
