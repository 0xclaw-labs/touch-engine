import test from "node:test";
import assert from "node:assert/strict";

test("types 与 ApiFailure 可导入且行为正确", async () => {
  const { ApiFailure } = await import("../src/apiError.ts");
  const err = new ApiFailure("bad request", 400, "invalid_json");
  assert.equal(err.status, 400);
  assert.equal(err.code, "invalid_json");
  assert.equal(err.message, "bad request");
  assert.equal(err instanceof Error, true);

  const types = await import("../src/types.ts");
  assert.equal(typeof types, "object");
});
