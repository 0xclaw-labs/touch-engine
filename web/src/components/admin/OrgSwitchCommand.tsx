"use client";

// 组织切换 Command：在受控成员列表内搜索并切换当前组织。
// 候选只来自 memberships 接口下发的可用成员；停用/非成员来源不在本面板出现。

import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { usable, type MembershipScope } from "@/lib/workspace-tenant";

export function OrgSwitchCommand({
  scopes,
  currentTenantId,
  onSelect,
  disabled,
}: {
  scopes: MembershipScope[];
  currentTenantId: string;
  onSelect: (tenantId: string) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const current = usable(scopes).find(
    (item) => item.tenant_id === currentTenantId,
  );

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  // 打开后焦点直接进入搜索框，键盘无需再 Tab。
  useEffect(() => {
    if (!open) return;
    setActiveIndex(0);
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(timer);
  }, [open, query]);

  const q = query.trim().toLowerCase();
  const options = usable(scopes).filter(
    (item) =>
      !q ||
      item.display_name.toLowerCase().includes(q) ||
      item.role.toLowerCase().includes(q),
  );

  const choose = (item: MembershipScope) => {
    onSelect(item.tenant_id);
    setOpen(false);
    setQuery("");
  };

  const onInputKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (options.length === 0) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveIndex((idx) => (idx + 1) % options.length);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveIndex((idx) => (idx - 1 + options.length) % options.length);
    } else if (event.key === "Enter") {
      event.preventDefault();
      const picked = options[activeIndex];
      if (picked) choose(picked);
    }
  };

  return (
    <div ref={rootRef} className="relative">
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="w-full justify-between"
      >
        <span className="truncate">{current?.display_name ?? "选择组织"}</span>
        <ChevronDown className="h-4 w-4 shrink-0" />
      </Button>
      {open ? (
        <Command className="absolute left-0 top-full z-50 mt-1 w-64">
          <CommandInput
            ref={inputRef}
            placeholder="搜索组织…"
            aria-label="搜索组织"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={onInputKeyDown}
          />
          <CommandList
            aria-activedescendant={
              options[activeIndex]
                ? `org-option-${options[activeIndex].tenant_id}`
                : undefined
            }
          >
            {options.length === 0 ? (
              <CommandEmpty>没有匹配的组织</CommandEmpty>
            ) : (
              <CommandGroup heading="我的组织">
                {options.map((item, index) => (
                  <CommandItem
                    key={item.tenant_id}
                    id={`org-option-${item.tenant_id}`}
                    selected={item.tenant_id === currentTenantId}
                    aria-selected={item.tenant_id === currentTenantId}
                    className={index === activeIndex ? "bg-accent" : undefined}
                    onSelect={() => choose(item)}
                  >
                    <span className="flex-1 truncate">{item.display_name}</span>
                    <span className="text-xs text-muted-foreground">
                      {item.role}
                    </span>
                    {item.tenant_id === currentTenantId ? (
                      <Check className="h-4 w-4" />
                    ) : null}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      ) : null}
    </div>
  );
}
