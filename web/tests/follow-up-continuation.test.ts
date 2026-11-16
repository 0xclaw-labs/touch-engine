import { expect, it, vi } from "vitest";
import { createHandoffRequest, continuationDestination } from "../src/lib/follow-up-continuation";
import { consumeSsoWorkspaceReset, resetWorkspaceForAccountChange, SSO_ACCOUNT_EPOCH_KEY } from "../src/lib/sso-workspace-reset";
import { currentTenantOp, resolveWorkspaceTenant, TENANT_STORAGE_KEY } from "../src/lib/workspace-tenant";
it("reuses StrictMode request but changing handoff gets its own original source", async () => { const issue = vi.fn(async (h: string) => ({ source: h })); const request = createHandoffRequest(issue); const a = request("h-a"); expect(request("h-a")).toBe(a); const b = request("h-b"); expect(b).not.toBe(a); expect(await b).toEqual({ source: "h-b" }); expect(issue).toHaveBeenCalledTimes(2); });
it("unknown normal shell handoff returns to the real root workspace", () => { expect(continuationDestination({ known: false })).toBe("/"); expect(continuationDestination({ known: true, path: "/work/stores/original" })).toBe("/work/stores/original"); });
it("a normal successful login or logout also invalidates old workspace requests without an SSO flag", () => {
    const saved = new Map([[TENANT_STORAGE_KEY, "old-account-tenant"]]);
    const memory = { getItem: (key: string) => saved.get(key) ?? null, setItem: (key: string, value: string) => { saved.set(key, value); }, removeItem: (key: string) => { saved.delete(key); } };
    const before = currentTenantOp();
    resetWorkspaceForAccountChange(memory);
    expect(currentTenantOp()).toBeGreaterThan(before);
    expect(memory.getItem(TENANT_STORAGE_KEY)).toBeNull();
    expect(memory.getItem(SSO_ACCOUNT_EPOCH_KEY)).toBeTruthy();
});
it("broadcasts every SSO account change even with no saved workspace", () => {
    const saved = new Map<string, string>();
    const memory = { getItem: (key: string) => saved.get(key) ?? null, setItem: (key: string, value: string) => { saved.set(key, value); }, removeItem: (key: string) => { saved.delete(key); } };
    consumeSsoWorkspaceReset("touch_sso_reset=1", memory, () => undefined);
    const first = memory.getItem(SSO_ACCOUNT_EPOCH_KEY);
    expect(first).toBeTruthy();
    consumeSsoWorkspaceReset("touch_sso_reset=1", memory, () => undefined);
    expect(memory.getItem(SSO_ACCOUNT_EPOCH_KEY)).not.toBe(first);
    expect(memory.getItem(TENANT_STORAGE_KEY)).toBeNull();
});
it("new SSO account clears old tenant memory and invalidates outstanding tenant confirmation", () => { const saved = new Map([[TENANT_STORAGE_KEY, "old-account-tenant"]]); const memory = { getItem: (k: string) => saved.get(k) || null, setItem: (k: string, v: string) => { saved.set(k, v); }, removeItem: (k: string) => { saved.delete(k); } }; const expire = vi.fn(); const before = currentTenantOp(); expect(consumeSsoWorkspaceReset("touch_sso_reset=1; other=1", memory, expire)).toBe(true); expect(currentTenantOp()).toBeGreaterThan(before); expect(memory.getItem(TENANT_STORAGE_KEY)).toBeNull(); expect(expire).toHaveBeenCalledOnce(); const scopes = ["one", "two"].map(tenant_id => ({ tenant_id, display_name: tenant_id, role: "org_owner", enabled: true, source: "membership" })); expect(resolveWorkspaceTenant(scopes, memory.getItem(TENANT_STORAGE_KEY))).toEqual({ status: "choose", options: scopes }); expect(consumeSsoWorkspaceReset("other=1", memory, expire)).toBe(false); });
