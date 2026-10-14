"use client";

import { useParams } from "next/navigation";
import { useEffect, useState } from "react";

import { touchEntryFace, touchEntryRedirect } from "@/lib/touchpoints";

// HUI-1665 /t/{code} 稳定入口面:查公共解析端点,拿到活动码就临时跳到既有 /c 页。
// 纪律:location.replace 是客户端临时跳转,绝不声称为永久重定向;
// 只有服务端白名单状态 + 合法码形才跳;别名停用/不存在给本地文案,不冒充成功;
// 公开面不携带任何后台凭据,后台字段名一律不出现。
export function TouchEntry() {
  const params = useParams<{ code: string }>();
  const [face, setFace] = useState<string | null>(null);
  const code = params?.code;

  useEffect(() => {
    if (typeof code !== "string" || code === "") {
      setFace(touchEntryFace("not_found"));
      return;
    }
    let alive = true;
    fetch(`/api/public/touchpoints/${encodeURIComponent(code)}`)
      .then(async (res) => {
        const data = (await res.json().catch(() => ({}))) as { state?: unknown; code?: unknown };
        if (!alive) return;
        const target = res.ok ? touchEntryRedirect(data) : null;
        if (target) {
          window.location.replace(target);
          return;
        }
        setFace(touchEntryFace(data?.state));
      })
      .catch(() => {
        if (alive) setFace(touchEntryFace(undefined));
      });
    return () => {
      alive = false;
    };
  }, [code]);

  if (face === null) {
    return <p>正在打开…</p>;
  }
  return <p>{face}</p>;
}
