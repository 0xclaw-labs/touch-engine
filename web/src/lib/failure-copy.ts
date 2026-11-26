// HUI-2628 fix2：错误面产品语句。状态码/机器错误码/后端原文不出现在用户界面，
// 每条都带下一步动作（gate-r2 盲评 fail 项：`没有完成(400 store_required)` 直出红字）。
// server 侧机器 token → 产品语义的映射只收口在这里；新增错误码在这里补产品语句。
export function failureText(status: number, data: Record<string, unknown>): string {
  if (data.error === "follow_up_target_unavailable") return "获客详情入口当前不可用或尚未获平台登记。请稍后重试，原线索仍保留在列表中。";
  if (data.error === "follow_up_unavailable") return "暂时不能进入这条线索：需要接收方确认入库、当前获客成员权限与顾客许可都可核验。请刷新或稍后重试。";
  if (data.error === "source_unknown") return "这条线索的原门店归属还无法核验。请让负责人核对来源，暂不能进入跟进。";

  if (data.error === "store_required") {
    return "这次没有完成：活动还没有关联门店。先到「门店」添加或选好门店，再操作一次。";
  }
  if (data.error === "not_member" || data.error === "membership_required") {
    return "当前账号尚未开通该组织的团队席位，暂无法进入。请联系管理员开通，或前往统一门户切换其他已开通账号。";
  }
  if (status === 401) {
    return "当前登录状态已失效。请重新登录后继续原来的操作。";
  }
  if (status === 403) {
    return "当前账号没有执行此操作的权限，或原授权已被收回。请让负责人核对授权后再试。";
  }
  if (status === 404) {
    return "要找的内容不在了，可能已被删除。回到列表刷新看看其他内容。";
  }
  if (status >= 500) {
    return "服务暂时没有响应。请稍等一下再重试；反复出现请联系平台。";
  }
  return "这次操作没有完成。请检查填写的内容，再试一次。";
}

// 登录面专用口径：wrong-password 401 是登录语境的主剧本（API 面的 401 是
// 「会话过期」语境，两句不能混）。其余状态一律回落 failureText 收口。
export function loginFailureText(status: number, data: Record<string, unknown>): string {
  if (status === 401) {
    return "邮箱或密码不对。请再试一次；忘记密码请联系为你开通账号的平台管理员。";
  }
  return failureText(status, data);
}
