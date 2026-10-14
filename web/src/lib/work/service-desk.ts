/**
 * HUI-2222：「确认与核销」入口的可见性判定（唯一判定点，两个导航槽位共用）。
 *
 * 开关来自 whoami features.service_desk（FEATURE_SERVICE_DESK，默认 off）。
 * off / 缺省一律不渲染：页面本体归 HUI-3562，未开通不冒充可用，
 * 也不用「正在建设」掩盖服务状态。
 *
 * 角色集对齐 server/internal/authz 的 ActionUpdate 矩阵：
 * org_owner 恒可、store_manager 本门店、staff T0 语义；agent 是只读代运营，
 * 不进核销面。未知/空角色一律不可见（fail-closed）。
 */

export const SERVICE_DESK_HREF = "/work/service";
export const SERVICE_DESK_LABEL = "确认与核销";

export type SessionFeatures = { serviceDesk: boolean } | undefined;

const SERVICE_DESK_ROLES: ReadonlySet<string> = new Set(["org_owner", "store_manager", "staff"]);

export function serviceDeskEntryVisible(features: SessionFeatures, role: string): boolean {
  if (!features?.serviceDesk) return false;
  return SERVICE_DESK_ROLES.has(role);
}
