"use client";

import { useRef, useState } from "react";

type Person = { principal_ref: string; display_name: string };
type Api = (method: string, path: string, body?: unknown) => Promise<{ ok: boolean; status: number; data: Record<string, unknown> }>;

// Exact-email choice consumes the server-controlled original Identity probe.
// Editing or starting another lookup always invalidates the former selection.
export function PersonPicker({ api, onSelect }: { api: Api; onSelect: (person: Person | null) => void }) {
  const [email, setEmail] = useState("");
  const [person, setPerson] = useState<Person | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const version = useRef(0);
  async function lookup() {
    const seq = ++version.current;
    onSelect(null); setPerson(null); setBusy(true); setNote("");
    try {
      const res = await api("POST", "admin/members/lookup", { email });
      if (seq !== version.current) return;
      if (!res.ok) {
        setNote(typeof res.data.message === "string" ? res.data.message : "人员查询未完成，请稍后重试或联系平台恢复人员服务。");
        return;
      }
      const candidate = res.data as unknown as Person;
      setPerson(candidate);
      setNote("请核对登录邮箱后选择人员。该操作不会自动授权。");
    } catch {
      if (seq === version.current) setNote("没有连上人员服务，请稍后重试。");
    } finally { if (seq === version.current) setBusy(false); }
  }
  return <fieldset className="tk-form">
    <legend>选择人员</legend>
    <label>对方的完整登录邮箱
      <input className="tk-input" type="email" autoComplete="off" value={email} onChange={(e) => {
        ++version.current; setEmail(e.target.value); setPerson(null); onSelect(null); setNote(""); setBusy(false);
      }} />
    </label>
    <button className="tk-quiet" type="button" disabled={busy || !email.includes("@")} onClick={() => void lookup()}>{busy ? "查询中…" : "核对人员"}</button>
    {person ? <button className="tk-button" type="button" onClick={() => { onSelect(person); setNote(`已选择 ${person.display_name}`); }}>选择 {person.display_name}</button> : null}
    {note ? <p role="status">{note}</p> : null}
    <p className="tk-note">只支持负责人按已知完整邮箱办理。对方尚无账号时，请先完成公共账号注册后重试；人员服务故障请联系平台恢复连接。</p>
  </fieldset>;
}
