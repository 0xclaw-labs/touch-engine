"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

interface StoreRec {
  id: string;
  name: string;
  address: string;
  status: "active" | "disabled";
}

export default function StoresPage() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState({ name: "", address: "" });
  const [edit, setEdit] = useState<{ id: string; name: string; address: string } | null>(null);
  const owner = session.role === "org_owner";

  const load = useCallback(async () => {
    setPhase("loading");
    const res = await session.api("GET", "stores");
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    // 修复轮 4 守卫:行形态防御(非对象/缺 id 的行直接丢弃,任何上游脏数据都
    // 不再把列表页打成白屏;正式重构归 2628)。
    const raw = Array.isArray(res.data.items) ? (res.data.items as unknown[]) : [];
    const items = raw.filter(
      (it): it is StoreRec =>
        typeof it === "object" && it !== null &&
        typeof (it as StoreRec).id === "string" && (it as StoreRec).id !== "",
    );
    setStores(items);
    setPhase(items.length === 0 ? "empty" : "ready");
  }, [session]);

  useEffect(() => {
    void load().catch(() => setPhase("error"));
  }, [load]);

  async function createStore(event: React.FormEvent) {
    event.preventDefault();
    const res = await session.api("POST", "stores", draft);
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setDraft({ name: "", address: "" });
    setError("");
    await load();
  }

  async function saveEdit(event: React.FormEvent) {
    event.preventDefault();
    if (!edit) return;
    const res = await session.api("PATCH", `stores/${edit.id}`, { name: edit.name, address: edit.address });
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setEdit(null);
    setError("");
    await load();
  }

  async function setStatus(id: string, status: "active" | "disabled") {
    const res = await session.api("POST", `stores/${id}/status`, { status });
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setError(status === "disabled" ? "门店已停用。新建活动会被拒绝，顾客页会标门店暂不可用。已有活动不会被自动改掉。" : "");
    await load();
  }

  return (
    <main>
      <h1 className="tk-title">门店</h1>
      <p className="tk-lead">先确认顾客会走进哪一家店。停用只挡住新活动，不改已经发出去的活动。</p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {phase === "empty" ? <p className="tk-state" data-state="empty">还没有门店。总部可以在下面登记第一家。</p> : null}
      {error && phase !== "error" ? <p className="tk-warn">{error}</p> : null}
      <ul className="tk-list">
        {stores.map((store) => (
          <li key={store.id}>
            {edit?.id === store.id ? (
              <form className="tk-form" onSubmit={saveEdit}>
                <input className="tk-input" value={edit.name} onChange={(event) => setEdit({ ...edit, name: event.target.value })} required />
                <input className="tk-input" value={edit.address} onChange={(event) => setEdit({ ...edit, address: event.target.value })} placeholder="地址" />
                <div className="tk-row">
                  <button className="tk-button" type="submit">保存</button>
                  <button className="tk-quiet" type="button" onClick={() => setEdit(null)}>取消</button>
                </div>
              </form>
            ) : (
              <>
                <strong>{store.name}</strong>
                <span className="tk-note"> {store.address || "无地址"} </span>
                {store.status === "disabled" ? <span className="tk-unknown">已停用</span> : <span className="tk-note">启用</span>}
                {owner ? (
                  <div className="tk-row tk-gap">
                    <button className="tk-quiet" type="button" onClick={() => setEdit({ id: store.id, name: store.name, address: store.address })}>编辑</button>
                    {store.status === "active" ? (
                      <button className="tk-quiet" type="button" onClick={() => void setStatus(store.id, "disabled")}>停用</button>
                    ) : (
                      <button className="tk-quiet" type="button" onClick={() => void setStatus(store.id, "active")}>启用</button>
                    )}
                  </div>
                ) : null}
              </>
            )}
          </li>
        ))}
      </ul>
      {owner ? (
        <form className="tk-form tk-section" onSubmit={createStore}>
          <h2 className="tk-section-title">登记门店</h2>
          <input className="tk-input" placeholder="门店名" value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} required />
          <input className="tk-input" placeholder="地址，可空" value={draft.address} onChange={(event) => setDraft({ ...draft, address: event.target.value })} />
          <button className="tk-button" type="submit">新增门店</button>
        </form>
      ) : (
        <p className="tk-note">门店的增删改由总部操作。你现在看到的是自己能管辖的店。</p>
      )}
    </main>
  );
}
