"use client";

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { AppSwitchResult } from "./use-app-switch";
import { agentWorkBanner, commitTenantSwitch, payerLabel } from "@/lib/eco-nav/touch-shell";
import {
  billingBadgeText,
  canLaunchManually,
  LAUNCH_UNRESOLVED_RESIDUAL,
  partitionApps,
  payerText,
  pinTouchApp,
  selectTenant,
  switchableApps,
  type EcoNavModel,
  type EcoNavViewport,
  type LaunchAvailability,
  type VisibleApp,
} from "@/lib/eco-nav/model";
import styles from "./eco-top-nav.module.css";

function useNarrow(enabled: boolean): boolean {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => {
    if (!enabled || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(max-width: 1100px)");
    const apply = () => setNarrow(media.matches);
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [enabled]);
  return narrow;
}

function AppControl({
  app,
  canSwitch,
  available,
  launching,
  onLaunch,
  inMenu = false,
}: {
  app: VisibleApp;
  canSwitch: boolean;
  available: LaunchAvailability;
  launching: boolean;
  onLaunch: (app: VisibleApp) => void;
  inMenu?: boolean;
}) {
  const enabled = canLaunchManually(app, canSwitch, available);
  const role = inMenu ? "menuitem" : undefined;
  const common = {
    "data-testid": `eco-app-${app.app_id}`,
    "data-nav-intent": "manual_switch",
    "data-creates-handoff": "false",
    "data-launch-mode": app.launch_mode,
    "data-launch-target-id": app.launch_target_id ?? "",
  } as const;
  if (!enabled) {
    return (
      <span
        className={styles.quiet}
        role={role}
        {...common}
        data-launch-residual="unresolved_launch_target"
        title={LAUNCH_UNRESOLVED_RESIDUAL}
      >
        {app.display_name}
      </span>
    );
  }
  return (
    <button
      type="button"
      className={styles.link}
      role={role}
      {...common}
      disabled={launching}
      aria-busy={launching || undefined}
      onClick={() => onLaunch(app)}
    >
      {app.display_name}
    </button>
  );
}

const notRegistered: LaunchAvailability = () => false;

export function EcoTopNav({
  model,
  nickname,
  sessionRole,
  viewport,
  resolveLaunchTarget = notRegistered,
  onSwitchApp,
  onTenantChange,
  onLogout,
  honorScopeMerchant = false,
}: {
  model: EcoNavModel;
  nickname: string;
  sessionRole: string;
  viewport?: EcoNavViewport;
  /** Availability only; never a URL. */
  resolveLaunchTarget?: LaunchAvailability;
  /** The BFF seam. Missing = switching stays disabled (preview/tests). */
  onSwitchApp?: (appId: string, targetId: string) => Promise<AppSwitchResult>;
  onTenantChange?: (tenantId: string) => void;
  onLogout?: () => void;
  /** 仅预览页打开。商家后台不把夹具租户写进会话。 */
  honorScopeMerchant?: boolean;
}) {
  const [nav, setNav] = useState(() => pinTouchApp(model));
  const [menuOpen, setMenuOpen] = useState(false);
  const [userOpen, setUserOpen] = useState(false);
  const [launching, setLaunching] = useState<Record<string, boolean>>({});
  const [launchError, setLaunchError] = useState("");
  const overflowRef = useRef<HTMLButtonElement>(null);
  const userRef = useRef<HTMLButtonElement>(null);
  const autoNarrow = useNarrow(viewport === undefined);
  const narrow = viewport ? viewport === "narrow" : autoNarrow;

  useEffect(() => {
    setNav(pinTouchApp(model));
    setMenuOpen(false);
    setUserOpen(false);
    setLaunchError("");
  }, [model]);

  if (!nav.renderable || !nav.brand) return null;

  const apps = switchableApps(nav.apps, nav.current_app.app_id);
  const parts = partitionApps(apps, narrow ? "narrow" : "wide");
  const canSwitch = nav.capabilities.can_switch_app === true;
  const scopeTrusted = honorScopeMerchant || nav.provenance === "public_ai_context";
  const payer = scopeTrusted ? payerText(nav) : payerLabel(nav.provenance, payerText(nav));
  const merchant = nav.scopes.find((item) => item.tenant_id === nav.active_tenant_id)?.display_name ?? null;
  const banner = agentWorkBanner(sessionRole, scopeTrusted ? merchant : null);
  const unresolved = [...parts.pinned, ...parts.overflow].some(
    (app) => !canLaunchManually(app, canSwitch, resolveLaunchTarget),
  );

  async function switchTo(appId: string, targetId: string) {
    if (!onSwitchApp) {
      setLaunchError("应用切换没有接通，没有跳转。");
      return;
    }
    if (launching[appId]) return;
    setLaunching((current) => ({ ...current, [appId]: true }));
    setLaunchError("");
    try {
      const result = await onSwitchApp(appId, targetId);
      if (!result.ok) {
        setLaunchError(result.message);
        return;
      }
      window.location.assign(result.launchUrl);
    } finally {
      setLaunching((current) => ({ ...current, [appId]: false }));
    }
  }

  function chooseTenant(tenantId: string) {
    setNav((current) => {
      const next = selectTenant(current, tenantId);
      if (next.active_tenant_id === current.active_tenant_id) return next;
      const committed = honorScopeMerchant
        ? next.active_tenant_id
        : commitTenantSwitch(next.provenance, next.active_tenant_id);
      if (committed) onTenantChange?.(committed);
      return next;
    });
  }

  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== "Escape") return;
    if (!menuOpen && !userOpen) return;
    if (menuOpen) overflowRef.current?.focus();
    else userRef.current?.focus();
    setMenuOpen(false);
    setUserOpen(false);
  }

  return (
    <header
      className={styles.bar}
      aria-label="生态导航"
      data-testid="eco-top-nav"
      data-viewport={narrow ? "narrow" : "wide"}
      data-provenance={nav.provenance}
      data-theme={nav.brand.theme.token_set_ref}
      onKeyDown={onKeyDown}
    >
      <div className={styles.brand} data-testid="eco-brand">
        <span className={styles.brandName} title={nav.brand.display_name}>
          {nav.brand.display_name}
        </span>
        <span
          className={styles.currentApp}
          data-testid="eco-current-app"
          data-app-id={nav.current_app.app_id}
          aria-current="page"
          title={nav.current_app.display_name}
        >
          {nav.current_app.display_name}
        </span>
      </div>

      <div className={styles.apps}>
        <div className={styles.pinned} data-testid="eco-app-pinned">
          {parts.pinned.map((app) => (
            <AppControl
              key={app.app_id}
              app={app}
              canSwitch={canSwitch}
              available={resolveLaunchTarget}
              launching={Boolean(launching[app.app_id])}
              onLaunch={(next) => {
                if (next.launch_target_id) void switchTo(next.app_id, next.launch_target_id);
              }}
            />
          ))}
        </div>
        {parts.overflow.length > 0 ? (
          <div className={styles.popover}>
            <button
              ref={overflowRef}
              type="button"
              className={styles.button}
              aria-expanded={menuOpen}
              aria-haspopup="menu"
              onClick={() => setMenuOpen((open) => !open)}
            >
              应用
            </button>
            {menuOpen ? (
              <div className={`${styles.menu} ${styles.menuLeft}`} role="menu" data-testid="eco-app-menu">
                {parts.overflow.map((app) => (
                  <AppControl
                    key={app.app_id}
                    app={app}
                    canSwitch={canSwitch}
                    available={resolveLaunchTarget}
                    launching={Boolean(launching[app.app_id])}
                    onLaunch={(next) => {
                      if (next.launch_target_id) void switchTo(next.app_id, next.launch_target_id);
                    }}
                    inMenu
                  />
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
        {unresolved ? (
          <p className={styles.srOnly} data-testid="eco-launch-residual">
            {LAUNCH_UNRESOLVED_RESIDUAL}
          </p>
        ) : null}
        {launchError ? (
          <p className={styles.launchError} role="alert" data-testid="eco-launch-error">{launchError}</p>
        ) : null}
      </div>

      {nav.return_context ? (
        <button
          type="button"
          className={styles.link}
          data-testid="eco-return-chip"
          data-nav-intent="return_source"
          data-creates-handoff="false"
          data-launch-target-id={nav.return_context.return_target_id}
          disabled={!onSwitchApp || launching[nav.return_context.source_app_id]}
          onClick={() => void switchTo(nav.return_context?.source_app_id ?? "", nav.return_context?.return_target_id ?? "")}
        >
          {nav.return_context.label}
        </button>
      ) : null}

      <div className={styles.spacer} />

      <div className={styles.context} data-testid="eco-nav-context">
        {banner ? (
          <span data-testid="eco-agent-banner" title={banner}>
            {banner}
          </span>
        ) : null}
        {scopeTrusted && nav.capabilities.can_switch_tenant && nav.scopes.length > 1
          ? nav.scopes.map((item) => {
              const name = item.display_name ?? item.tenant_id;
              return (
                <button
                  key={item.tenant_id}
                  type="button"
                  className={item.tenant_id === nav.active_tenant_id ? `${styles.button} ${styles.pressed}` : styles.button}
                  data-testid={`eco-tenant-${item.tenant_id}`}
                  aria-pressed={item.tenant_id === nav.active_tenant_id}
                  title={name}
                  onClick={() => chooseTenant(item.tenant_id)}
                >
                  <span className={styles.ellipsis}>{name}</span>
                </button>
              );
            })
          : null}
        <span className={styles.ellipsis} data-testid="eco-payer" title={payer}>
          {payer === "付款主体需确认" ? payer : `付款主体：${payer}`}
        </span>
        <span data-testid="eco-billing">{billingBadgeText(nav)}</span>
        {nav.provenance !== "public_ai_context" ? (
          <span className={styles.badge} data-testid="eco-provenance">
            {nav.status.summary || "预览上下文 · 未接公共身份"}
          </span>
        ) : null}
      </div>

      <div className={styles.right}>
        <div className={styles.popover}>
          <button
            ref={userRef}
            type="button"
            className={styles.button}
            aria-haspopup="menu"
            aria-expanded={userOpen}
            aria-label={`账户 ${nickname}`}
            onClick={() => setUserOpen((open) => !open)}
          >
            <span className={styles.ellipsis}>{nickname}</span>
          </button>
          {userOpen ? (
            <div className={styles.menu} role="menu">
              <p className={styles.roleLine}>{nav.role_label || "角色需确认"}</p>
              {onLogout ? (
                <button type="button" className={styles.button} onClick={onLogout}>
                  退出
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </header>
  );
}
