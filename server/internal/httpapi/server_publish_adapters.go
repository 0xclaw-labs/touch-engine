// server_publish_adapters.go: HUI-4585/4586/4587/4588 平台适配器注册与快手
// OAuth 回跳端点(本票在 httpapi 的唯一窄补丁面;装配只新增一行注册调用 +
// 一行路由登记,见 server.go)。
//
// 注册纪律(1670 冻结契约 §8):每平台至多一个 adapter,SetAdapter 后注册
// 替换;凭据不齐=适配器不注册——平台自动化通道诚实回退为 1670 的
// manual_export 说明面("自动交接通道未接入"),绝不编造入口。
//
// OAuth 回跳(4587):GET /api/v1/public/handoff-callbacks/kuaishou/oauth 是
// 浏览器 302 回跳(快手授权页把 code+state 送回本服务),与 POST
// /api/v1/public/handoff-callbacks/{platform}(平台→服务端验签回调)同前缀、
// 不同方法/段,无 ServeMux 冲突。state 校验/token 交换全部在 adapter
// (FinishOAuth),本 handler 只做回跳形状收尾。
package httpapi

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff/adapters"
)

// registerPublishAdapters wires the platform adapters from deployment config
// (4585-4588). Missing credentials = adapter NOT registered (honest fallback).
func registerPublishAdapters(database *sql.DB, srv *Server, cfg config.Config) {
	// 4585 抖音:官方 H5 关联(share_id)与专项任务小程序路径。
	if strings.TrimSpace(cfg.PublishHandoffDouyinClientKey) != "" {
		srv.PublishHandoff.SetAdapter(adapters.NewDouyinAdapter(database, adapters.DouyinConfig{
			ClientKey:   cfg.PublishHandoffDouyinClientKey,
			AppRef:      cfg.PublishHandoffAppRef,
			MiniappPath: cfg.PublishHandoffDouyinMiniappPath,
		}))
	}
	// 4586 小红书:官方 JS 媒体交接(公开鉴权参数;无 secret 进浏览器)。
	if strings.TrimSpace(cfg.PublishHandoffXhsAppID) != "" {
		srv.PublishHandoff.SetAdapter(adapters.NewXiaohongshuAdapter(database, adapters.XhsPublicConfig{
			AppID: cfg.PublishHandoffXhsAppID,
		}))
	}
	// 4587 快手:网站 OAuth+服务端投稿(secret 仅服务器)。
	if strings.TrimSpace(cfg.PublishHandoffKsClientID) != "" && strings.TrimSpace(cfg.PublishHandoffKsClientSecret) != "" {
		srv.PublishHandoff.SetAdapter(adapters.NewKuaishouAdapter(database, adapters.KuaishouConfig{
			ClientID:       cfg.PublishHandoffKsClientID,
			ClientSecret:   cfg.PublishHandoffKsClientSecret,
			AppRef:         cfg.PublishHandoffAppRef,
			AuthorizeBase:  firstNonEmptyStr(cfg.PublishHandoffKsAuthorizeBase, "https://open.kuaishou.com/oauth2/authorize"),
			ReturnBase:     cfg.PublishHandoffKsReturnBase,
			AuthorizeScope: "user_video_publish,user_video_info",
		}))
	}
	// 4588 手动家族:无凭据依赖,恒注册(逐平台准确步骤/差异说明/建议标签)。
	for _, p := range adapters.ManualFamilyPlatforms {
		srv.PublishHandoff.SetAdapter(adapters.NewManualAdapter(p))
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// handleKuaishouOAuthCallback serves GET
// /api/v1/public/handoff-callbacks/kuaishou/oauth — the OAuth redirect
// return (browser user-agent; 302 back to the customer page). code/state
// 校验与 token 交换全部在 adapter.FinishOAuth;本函数不碰凭据。
func (s *Server) handleKuaishouOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.publishHandoffGate(w) {
		return
	}
	adapter := s.PublishHandoff.AdapterFor("kuaishou")
	finisher, ok := adapter.(adapters.OAuthFinisher)
	if !ok {
		// 未注册=本部署未接快手 OAuth:诚实文本,不伪造回跳。
		fail(w, http.StatusUnprocessableEntity, "unsupported_by_adapter",
			"该部署未接入快手授权投稿;请回到交接页使用手动指引")
		return
	}
	q := r.URL.Query()
	code := strings.TrimSpace(q.Get("code"))
	state := strings.TrimSpace(q.Get("state"))
	returnTo, err := finisher.FinishOAuth(r.Context(), code, state)
	if err != nil {
		// 平台拒绝/取消/重放/过期:回跳原页带失败旗标(同源 https 已在
		// mint 时校验);无 return_to(如 state 未知)则诚实文本。
		if target, perr := oauthReturnWithError(returnTo, err); perr == nil {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "oauth_callback_refused",
			"message": "授权没有完成或已失效；请回到交接页重新发起授权。素材选择不会因此丢失。",
		})
		return
	}
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// oauthReturnWithError appends the failure flag onto the validated return
// address (same-origin https; checked at state mint time in the adapter).
func oauthReturnWithError(returnTo string, err error) (string, error) {
	returnTo = strings.TrimSpace(returnTo)
	if !strings.HasPrefix(returnTo, "https://") {
		return "", err
	}
	sep := "?"
	if strings.Contains(returnTo, "?") {
		sep = "&"
	}
	return returnTo + sep + "oauth=denied&reason=" + url.QueryEscape("authorization_failed"), nil
}
