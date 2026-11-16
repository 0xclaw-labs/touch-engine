"use client";
import { useEffect, useId, useRef, useState } from "react";

// Native dialog supplies focus confinement, Escape and return focus without
// blocking the browser or replacing the authorization preview with an alert.
export function useTaskReview() {
  const [message,setMessage] = useState<string | null>(null);
  const pending = useRef<((confirmed: boolean) => void) | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  useEffect(() => {
    if (message !== null && dialog.current && !dialog.current.open) dialog.current.showModal();
  },[message]);
  useEffect(() => () => { pending.current?.(false); pending.current=null; },[]);
  function requestReview(text: string): Promise<boolean> {
    pending.current?.(false);
    setMessage(text);
    return new Promise((resolve) => { pending.current=resolve; });
  }
  function settle(confirmed: boolean) {
    dialog.current?.close();
    pending.current?.(confirmed); pending.current=null; setMessage(null);
  }
  const review = <dialog className="tk-section" ref={dialog} aria-labelledby={title} onCancel={(event) => {event.preventDefault(); settle(false);}}>
    <h3 id={title}>核对本次办理</h3>
    {(message ?? "").split("\n").map((line,index) => <p key={index}>{line}</p>)}
    <div className="tk-row tk-gap">
      <button className="tk-button" type="button" data-action="confirm-review" onClick={() => settle(true)}>确认办理</button>
      <button className="tk-quiet" type="button" onClick={() => settle(false)}>返回修改</button>
    </div>
  </dialog>;
  return {requestReview,review};
}
