import { beginTenantOp, TENANT_STORAGE_KEY, type TenantMemory } from "@/lib/workspace-tenant";
export const SSO_ACCOUNT_EPOCH_KEY = "touch_sso_account_epoch";
export function resetWorkspaceForAccountChange(memory: TenantMemory): void {
    beginTenantOp();
    memory.removeItem(TENANT_STORAGE_KEY);
    memory.setItem(SSO_ACCOUNT_EPOCH_KEY, crypto.randomUUID());
}
export function consumeSsoWorkspaceReset(cookie: string, memory: TenantMemory, expire: () => void): boolean {
    if (!cookie.split(";").some(part => part.trim() === "touch_sso_reset=1"))
        return false;
    resetWorkspaceForAccountChange(memory);
    expire();
    return true;
}
export function resetWorkspaceAfterSso() {
    if (typeof window === "undefined")
        return;
    consumeSsoWorkspaceReset(document.cookie, window.localStorage, () => { document.cookie = "touch_sso_reset=; Path=/; Max-Age=0; SameSite=Lax; Secure"; });
}

export function observeSsoAccountChanges(onChange: () => void): () => void {
    let observed = window.localStorage.getItem(SSO_ACCOUNT_EPOCH_KEY);
    const check = () => {
        const next = window.localStorage.getItem(SSO_ACCOUNT_EPOCH_KEY);
        if (next === observed) return;
        observed = next;
        beginTenantOp();
        onChange();
    };
    const storage = (event: StorageEvent) => {
        if (event.storageArea === window.localStorage && event.key === SSO_ACCOUNT_EPOCH_KEY) check();
    };
    window.addEventListener("storage", storage);
    window.addEventListener("focus", check);
    return () => {
        window.removeEventListener("storage", storage);
        window.removeEventListener("focus", check);
    };
}
