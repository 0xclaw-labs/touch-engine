"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";

interface StoreRec {
  id: string;
  name: string;
  address: string;
  status: "active" | "disabled";
}

export default function StoresPage() {
  const session = useSession();
  const router = useRouter();
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState({ name: "", address: "" });
  const [addOpen, setAddOpen] = useState(false);
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
    if (items.length === 1 && new URLSearchParams(window.location.search).get("manage") !== "1") router.replace(`/work/stores/${encodeURIComponent(items[0].id)}`);
    setPhase(items.length === 0 ? "empty" : "ready");
  }, [session, router]);

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
    setAddOpen(false);
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
    <main className="tk-page">
      <h1 className="tk-title">门店</h1>
      <p className="tk-lead">先确认顾客会走进哪一家店。停用只挡住新活动，不改已经发出去的活动。</p>
      {phase === "loading" ? (
        <div className="tk-gap" data-state="loading" role="status">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-4/5" />
        </div>
      ) : null}
      {phase === "error" ? (
        <Alert variant="destructive" role="alert" data-state="error" className="tk-gap">
          <AlertDescription className="tk-row">
            <span>{error || surfaceLabel("error")}</span>
            <Button variant="outline" size="sm" type="button" data-action="retry" onClick={() => void load()}>重试</Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {phase === "empty" ? (
        <Card data-state="empty">
          <CardContent className="tk-gap pt-6">
            <p className="tk-note">还没有门店。总部可以在下面登记第一家。</p>
          </CardContent>
        </Card>
      ) : null}
      {error && phase !== "error" ? <Alert variant="warning" className="tk-gap"><AlertDescription>{error}</AlertDescription></Alert> : null}
      {phase === "ready" ? (
        <div className="tk-stack">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>门店</TableHead>
                <TableHead>地址</TableHead>
                <TableHead>状态</TableHead>
                {owner ? <TableHead>操作</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {stores.map((store) => (
                <TableRow key={store.id}>
                  <TableCell className="font-medium">
                    <Link href={`/work/stores/${encodeURIComponent(store.id)}`}>{store.name}</Link>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{store.address || "无地址"}</TableCell>
                  <TableCell>
                    <Badge variant={store.status === "disabled" ? "muted" : "success"}>
                      {store.status === "disabled" ? "已停用" : "启用"}
                    </Badge>
                  </TableCell>
                  {owner ? (
                    <TableCell>
                      <div className="tk-row tk-gap">
                        <Dialog
                          open={edit?.id === store.id}
                          onOpenChange={(open) => {
                            if (!open) setEdit(null);
                          }}
                        >
                          <DialogTrigger asChild>
                            <Button
                              variant="outline"
                              size="sm"
                              type="button"
                              data-action="open-edit-store"
                              onClick={() => setEdit({ id: store.id, name: store.name, address: store.address })}
                            >
                              编辑
                            </Button>
                          </DialogTrigger>
                          <DialogContent>
                            <DialogHeader>
                              <DialogTitle>编辑门店</DialogTitle>
                              <DialogDescription>调整门店名或地址；保存后顾客页按最新门店信息展示。</DialogDescription>
                            </DialogHeader>
                            {edit ? (
                              <form className="tk-form tk-gap" onSubmit={saveEdit}>
                                <Label className="sr-only" htmlFor="edit-store-name">门店名</Label>
                                <Input
                                  id="edit-store-name"
                                  value={edit.name}
                                  onChange={(event) => setEdit({ ...edit, name: event.target.value })}
                                  required
                                />
                                <Label className="sr-only" htmlFor="edit-store-address">地址</Label>
                                <Input
                                  id="edit-store-address"
                                  value={edit.address}
                                  onChange={(event) => setEdit({ ...edit, address: event.target.value })}
                                  placeholder="地址"
                                />
                                <DialogFooter>
                                  <Button type="submit" size="sm">保存</Button>
                                  <DialogClose asChild>
                                    <Button variant="outline" size="sm" type="button">取消</Button>
                                  </DialogClose>
                                </DialogFooter>
                              </form>
                            ) : null}
                          </DialogContent>
                        </Dialog>
                        <Link className="tk-quiet" href={`/work/stores/${store.id}/channels`}>渠道动作</Link>
                        <Link className="tk-quiet" href={`/work/stores/${store.id}/page`}>门店首页</Link>
                        {store.status === "active" ? (
                          <Button variant="outline" size="sm" type="button" onClick={() => void setStatus(store.id, "disabled")}>停用</Button>
                        ) : (
                          <Button variant="outline" size="sm" type="button" onClick={() => void setStatus(store.id, "active")}>启用</Button>
                        )}
                      </div>
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
      {owner ? (
        <Dialog open={addOpen} onOpenChange={setAddOpen}>
          <DialogTrigger asChild>
            <Button type="button" data-action="open-add-store">新增门店</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>登记门店</DialogTitle>
              <DialogDescription>填写门店名与地址，地址可空。保存后顾客按这家店看到活动。</DialogDescription>
            </DialogHeader>
            <form className="tk-form tk-gap" onSubmit={createStore}>
              <Label className="sr-only" htmlFor="store-name">门店名</Label>
              <Input
                id="store-name"
                placeholder="门店名"
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
                required
              />
              <Label className="sr-only" htmlFor="store-address">地址</Label>
              <Input
                id="store-address"
                placeholder="地址，可空"
                value={draft.address}
                onChange={(event) => setDraft({ ...draft, address: event.target.value })}
              />
              <DialogFooter>
                <Button type="submit">新增门店</Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      ) : (
        <p className="tk-note">门店的增删改由总部操作。你现在看到的是自己能管辖的店。</p>
      )}
    </main>
  );
}
