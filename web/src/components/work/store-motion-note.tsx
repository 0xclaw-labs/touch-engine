"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { presentDeclaration } from "@/lib/store-motion-status";
import { surfaceLabel } from "@/lib/product-finish";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";

interface StoreMotionParams {
  store_name: string;
  activity_time: string;
  price: string;
  address: string;
  offer_copy: string;
  cta: string;
}

interface RecordedMotion {
  status: string;
  note: string;
}

const EMPTY_PARAMS: StoreMotionParams = {
  store_name: "",
  activity_time: "",
  price: "",
  address: "",
  offer_copy: "",
  cta: "",
};

function textOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function recordedFrom(
  data: Record<string, unknown>,
): { params: StoreMotionParams; recorded: RecordedMotion } | null {
  const params = data.params;
  const declaration = data.declaration;
  if (
    !params ||
    typeof params !== "object" ||
    !declaration ||
    typeof declaration !== "object"
  )
    return null;
  const fields = params as Record<string, unknown>;
  const declarationFields = declaration as Record<string, unknown>;
  if (typeof fields.price !== "string") return null;
  if (typeof declarationFields.status !== "string") return null;
  if (typeof declarationFields.unchanged_store_note !== "string") return null;
  return {
    params: {
      store_name: textOf(fields.store_name),
      activity_time: textOf(fields.activity_time),
      price: fields.price,
      address: textOf(fields.address),
      offer_copy: textOf(fields.offer_copy),
      cta: textOf(fields.cta),
    },
    recorded: {
      status: declarationFields.status,
      note: declarationFields.unchanged_store_note,
    },
  };
}

export function StoreMotionNote({ campaignId }: { campaignId: string }) {
  const session = useSession();
  const [params, setParams] = useState<StoreMotionParams>(EMPTY_PARAMS);
  const [phase, setPhase] = useState<"loading" | "empty" | "ready" | "error">(
    "loading",
  );
  const [recorded, setRecorded] = useState<RecordedMotion | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const statusView = recorded ? presentDeclaration(recorded.status) : null;

  const load = useCallback(async () => {
    setPhase("loading");
    const res = await session.api(
      "GET",
      `campaigns/${campaignId}/store-motion`,
    );
    if (res.status === 404) {
      setRecorded(null);
      setError("");
      setPhase("empty");
      return;
    }
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    const view = recordedFrom(res.data);
    if (!view) {
      setError("没有完成（门店参数的返回不能显示）");
      setPhase("error");
      return;
    }
    setParams(view.params);
    setRecorded(view.recorded);
    setError("");
    setPhase("ready");
  }, [campaignId, session]);

  useEffect(() => {
    void load().catch(() => setPhase("error"));
  }, [load]);

  function setField(key: keyof StoreMotionParams, value: string) {
    setParams((current) => ({ ...current, [key]: value }));
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const res = await session.api(
        "PUT",
        `campaigns/${campaignId}/store-motion`,
        {
          store_name: params.store_name,
          activity_time: params.activity_time,
          price: params.price,
          address: params.address,
          offer_copy: params.offer_copy,
          cta: params.cta,
        },
      );
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      const view = recordedFrom(res.data);
      if (!view) {
        setError("没有完成（门店参数的返回不能显示）");
        return;
      }
      setParams(view.params);
      setRecorded(view.recorded);
      setPhase("ready");
    } catch {
      setError("没有完成（没有连上服务）");
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      {phase === "loading" ? (
        <div className="tk-gap" data-state="loading" role="status">
          <Skeleton className="h-6 w-1/3" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : null}
      {phase === "empty" ? (
        <Card className="" data-state="empty">
          <CardContent className="tk-gap pt-6">
            <p className="tk-note">还没有记下门店参数。</p>
          </CardContent>
        </Card>
      ) : null}
      {phase === "error" ? (
        <Alert variant="destructive" role="alert" data-state="error">
          <AlertDescription>{error || surfaceLabel("error")}</AlertDescription>
        </Alert>
      ) : null}
      {recorded ? (
        <Card>
          <CardContent className="tk-gap pt-5">
            {statusView ? (
              <p
                className="tk-note"
                data-field="declaration-status"
                data-state={statusView.tone}
              >
                <Badge
                  variant={statusView.tone === "pending" ? "warning" : "muted"}
                >
                  状态 {statusView.copy}
                </Badge>
              </p>
            ) : null}
            <p className="tk-note" data-field="unchanged-store-note">
              {recorded.note}
            </p>
          </CardContent>
        </Card>
      ) : null}
      {error && phase !== "error" ? (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {phase === "loading" ? null : (
        <Card>
          <CardContent className="tk-gap pt-5">
            <form className="tk-form" onSubmit={save}>
              <label className="tk-label">
                门店名
                <Input
                  name="store_name"
                  type="text"
                  value={params.store_name}
                  onChange={(event) =>
                    setField("store_name", event.target.value)
                  }
                  required
                />
              </label>
              <label className="tk-label">
                活动时间
                <Input
                  name="activity_time"
                  type="text"
                  value={params.activity_time}
                  onChange={(event) =>
                    setField("activity_time", event.target.value)
                  }
                  required
                />
              </label>
              <label className="tk-label">
                价格
                <Input
                  name="price"
                  type="text"
                  value={params.price}
                  onChange={(event) => setField("price", event.target.value)}
                  autoComplete="off"
                  required
                />
              </label>
              <label className="tk-label">
                地址
                <Input
                  name="address"
                  type="text"
                  value={params.address}
                  onChange={(event) => setField("address", event.target.value)}
                  required
                />
              </label>
              <label className="tk-label">
                优惠文案
                <Input
                  name="offer_copy"
                  type="text"
                  value={params.offer_copy}
                  onChange={(event) =>
                    setField("offer_copy", event.target.value)
                  }
                  required
                />
              </label>
              <label className="tk-label">
                CTA
                <Input
                  name="cta"
                  type="text"
                  value={params.cta}
                  onChange={(event) => setField("cta", event.target.value)}
                  required
                />
              </label>
              <Button type="submit" disabled={saving}>
                记下门店参数
              </Button>
            </form>
          </CardContent>
        </Card>
      )}
    </>
  );
}
