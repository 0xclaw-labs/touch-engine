/** A creation intent survives reload and lost responses. Only an explicit new-entry
 * action clears it; retries always send the frozen payload and original key. */
export interface StoreEntryRequest {
  store_id: string;
  label: string;
  position: string;
  creation_request_key: string;
}
export interface EntryIntent { request: StoreEntryRequest; completed?: boolean }
type IntentStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;
export function entryIntentStorageKey(tenant: string, actor: string, store: string) {
  return `touchpoint-entry:${tenant}:${actor}:${store}`;
}
export function readEntryIntent(storage: IntentStorage, key: string): EntryIntent | null {
  const raw = storage.getItem(key);
  if (!raw) return null;
  const value = JSON.parse(raw) as EntryIntent;
  if (!value.request?.creation_request_key || !value.request.store_id) throw new Error("入口请求无法恢复，请核对已有入口。");
  return value;
}
export function freezeEntryIntent(storage: IntentStorage, key: string, request: Omit<StoreEntryRequest, "creation_request_key">, mint: () => string): EntryIntent {
  const saved = readEntryIntent(storage, key);
  if (saved) return saved;
  const intent: EntryIntent = { request: { ...request, creation_request_key: mint() } };
  storage.setItem(key, JSON.stringify(intent));
  return intent;
}
