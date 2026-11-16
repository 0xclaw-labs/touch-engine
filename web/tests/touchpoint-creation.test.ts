import { describe, expect, it } from "vitest";
import { entryIntentStorageKey, freezeEntryIntent, readEntryIntent } from "../src/lib/touchpoint-creation";
describe("稳定入口请求恢复", () => {
 it("双击、丢响应后新页面和修改输入都重用冻结负载，只有明确另建才换码意图", () => {
  const rows = new Map<string,string>();
  const storage = {getItem:(k:string)=>rows.get(k)??null,setItem:(k:string,v:string)=>{rows.set(k,v)},removeItem:(k:string)=>{rows.delete(k)}};
  const key=entryIntentStorageKey("tenant","owner","store");
  const request={store_id:"store",label:"前台",position:"收银台"};
  const first=freezeEntryIntent(storage,key,request,()=>"original-key");
  expect(freezeEntryIntent(storage,key,{...request,label:"改过"},()=>"double-click")).toEqual(first);
  expect(readEntryIntent(storage,key)).toEqual(first);
  storage.removeItem(key);
  expect(freezeEntryIntent(storage,key,request,()=>"explicit-new").request.creation_request_key).toBe("explicit-new");
  expect(entryIntentStorageKey("tenant","manager","store")).not.toBe(key);
 });
});
