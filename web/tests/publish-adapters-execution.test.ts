import { afterEach, describe, expect, it, vi } from "vitest";
import { File as NodeFile } from "node:buffer";
import { handoffFailureMessage, loadMediaFiles, preparePlatformHandoff, sharePreparedFiles, sharePublicLink } from "../src/lib/publish-adapters/execution";
import { buildDouyinSchema } from "../src/lib/publish-adapters/douyin";
import { invokeXhsJs, registerXhsJsSdkLoader } from "../src/lib/publish-adapters/xiaohongshu";

afterEach(() => { vi.unstubAllGlobals(); registerXhsJsSdkLoader(undefined); });

describe("actual user handoff operations", () => {
  it("loads ordered actual bytes then shares those files from the user action", async () => {
    vi.stubGlobal("File", NodeFile);
    const fetcher = vi.fn(async (url: string) => new Response(url.endsWith("1") ? "first" : "second", { headers: { "Content-Type": "image/jpeg" } }));
    const files = await loadMediaFiles([{ kind:"image",url:"https://m.example/1",index:0 },{kind:"image",url:"https://m.example/2",index:1}], fetcher as unknown as typeof fetch);
    expect(await files[0].text()).toBe("first"); expect(await files[1].text()).toBe("second");
    const share = vi.fn(async () => {});
    const promise = sharePreparedFiles(files, {canShare: () => true, share});
    expect(share).toHaveBeenCalledWith({files}); // called before first await
    expect(await promise).toEqual({evidence:"handoff_only",operation:"system_share",officialResult:"unknown"});
  });
  it("keeps revoked media and cancelled/unsupported shares as recoverable failures", async () => {
    await expect(loadMediaFiles([{kind:"video",url:"https://m.example/v",index:0}], (async()=>new Response("",{status:403})) as typeof fetch)).rejects.toThrow(/失效/);
    vi.stubGlobal("File", NodeFile); const files=[new File(["bytes"],"v.mp4",{type:"video/mp4"})];
    const share=vi.fn(async()=>{throw new DOMException("cancel","AbortError")});
    await expect(sharePreparedFiles(files,{canShare:()=>true,share})).rejects.toMatchObject({name:"AbortError"});
    await expect(sharePreparedFiles(files,{canShare:()=>false,share})).rejects.toThrow(/不支持/);
    expect(share).toHaveBeenCalledTimes(1);
  });
  it("shares a public link without media/content pack", async()=>{
    const share=vi.fn(async()=>{});expect((await sharePublicLink("https://store.example/s/demo","门店",{share})).evidence).toBe("handoff_only");
    expect(share).toHaveBeenCalledWith({url:"https://store.example/s/demo",title:"门店"});
  });
  it("prepares a server-resolved link without fetching fake media and keeps cancellation recoverable", async()=>{
    const fetcher=vi.fn();vi.stubGlobal("fetch",fetcher);
    expect(await preparePlatformHandoff({type:"open_url",url:"https://store.example/s/demo"})).toEqual({files:[]});
    expect(fetcher).not.toHaveBeenCalled();
    const error=new DOMException("cancel","AbortError");
    const share=vi.fn(async()=>{throw error;});
    await expect(sharePublicLink("https://store.example/s/demo","",{share})).rejects.toBe(error);
    expect(handoffFailureMessage(error,"link_share")).toContain("链接仍在");
    expect(handoffFailureMessage(error,"link_share")).not.toContain("保存素材");
  });
  it("does not invoke native sharing for an unsafe or unavailable link",async()=>{
    const share=vi.fn(async()=>{});
    await expect(sharePublicLink("javascript:alert(1)","",{share})).rejects.toThrow(/无法分享/);
    expect(share).not.toHaveBeenCalled();
    await expect(sharePublicLink("https://store.example/s/demo","",{} as Pick<Navigator,"share">)).rejects.toThrow(/复制链接/);
  });
  it("invokes the approved XHS script's exact media-only API and reports failures",async()=>{
    registerXhsJsSdkLoader(undefined);const share=vi.fn();vi.stubGlobal("window",{xhs:{share}});vi.stubGlobal("document",{});
    const verify={appKey:"app",nonce:"fixture",timestamp:String(Date.now()),signature:"signature"};
    const payload={type:"normal" as const,video:"",cover:"",images:["https://m.example/1","https://m.example/2"]};
    const failure=vi.fn();await invokeXhsJs(payload,verify,failure);
    expect(share.mock.calls[0][0].shareInfo).toEqual(payload);expect(share.mock.calls[0][0].verifyConfig).toEqual(verify);
    share.mock.calls[0][0].fail({code:"download_failed"});expect(failure).toHaveBeenCalledWith(expect.stringContaining("交接失败"));
  });
  it("uses only documented signed Douyin schema keys and never local share IDs",()=>{
    const raw={type:"js_media_handoff",params:{sdk:"douyin_h5",client_key:"key",share_id:"official",state:"official",nonce_str:"n",timestamp:String(Math.floor(Date.now()/1000)),signature:"sig",share_expires_at:new Date(Date.now()+60000).toISOString(),video_path:"https://m.example/中文+视频.mp4",unexpected:"must not forward",secret:"must not forward"}};
    const schema=buildDouyinSchema(raw);expect(schema).toMatch(/^snssdk1128:\/\/openplatform\/share\?/);const q=new URL(schema!).searchParams;
    expect(q.get("video_path")).toBe(raw.params.video_path);expect(q.has("unexpected")).toBe(false);expect(q.has("secret")).toBe(false);
    expect(buildDouyinSchema({...raw,params:{...raw.params,signature:""}})).toBeNull();
  });
});
