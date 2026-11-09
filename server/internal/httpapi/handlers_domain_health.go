package httpapi

// HUI-2222 (wave2, B15 工程面): 运行面域名健康检查端点。
//
// 职责边界:只「如实探测」操作者经服务端配置声明的公开域名/媒体域名,
// 请求输入对目标零影响(无 query/body 注入面,天然无 SSRF 搬运)。它不是
// 公共跳转白名单——那道闸在 extrajump.OfficialAddress;本端点是工程观察面,
// 环回/局域网配置也如实报告(本地验收常态),不编造也不 censor。
//
// 诚实态:not_configured(缺配置)/same_origin(刻意的相对 BFF 路径配置)/
// invalid_config(配置不是可探测 URL)/reachable / unreachable(连接失败、
// 超时、4xx/5xx)。任一配置存在但不可达 → 整体 degraded;公开域名缺配置 →
// degraded(没有可证明健康的对象)。

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const domainHealthTimeout = 5 * time.Second

func domainHealthRFC3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

type domainTarget struct {
	kind string
	name string
	url  string
}

func (s *Server) domainHealthTargets() []domainTarget {
	return []domainTarget{
		{kind: "public_base", name: "PUBLIC_BASE_URL", url: strings.TrimSpace(s.Cfg.PublicBaseURL)},
		{kind: "media_base", name: "PUBLISH_HANDOFF_MEDIA_BASE_URL", url: strings.TrimSpace(s.Cfg.PublishHandoffMediaBaseURL)},
	}
}

func (s *Server) handleDomainHealth(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: domainHealthTimeout}
	targets := s.domainHealthTargets()
	items := make([]map[string]any, 0, len(targets))
	overall := "ok"
	for _, tg := range targets {
		item := map[string]any{"target_kind": tg.kind, "config_key": tg.name}
		if tg.url == "" {
			item["configured"] = false
			item["status"] = "not_configured"
			overall = "degraded"
			items = append(items, item)
			continue
		}
		item["configured"] = true
		// 相对路径(如默认 /api/media-handoffs)是刻意的同源 BFF 配置:媒体与
		// 公开域名同源,没有独立域名可探——如实报 same_origin,不算配置错误。
		if strings.HasPrefix(tg.url, "/") {
			item["status"] = "same_origin"
			item["detail"] = "relative BFF path; media rides the public origin"
			items = append(items, item)
			continue
		}
		parsed, err := url.Parse(tg.url)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			item["status"] = "invalid_config"
			item["error"] = "configured value is not a probeable absolute http(s) URL"
			overall = "degraded"
			items = append(items, item)
			continue
		}
		item["url"] = tg.url
		ctx, cancel := context.WithTimeout(r.Context(), domainHealthTimeout)
		start := time.Now()
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, tg.url, nil)
		if rerr != nil {
			cancel()
			item["status"] = "invalid_config"
			item["error"] = "configured value could not be turned into a request"
			overall = "degraded"
			items = append(items, item)
			continue
		}
		res, derr := client.Do(req)
		latency := time.Since(start).Milliseconds()
		cancel()
		item["latency_ms"] = latency
		item["checked_at"] = domainHealthRFC3339(start)
		if derr != nil {
			item["status"] = "unreachable"
			// 错误只留类别与底层 transport 文案(不含响应体;目标串已是配置值)。
			item["error"] = "probe failed: " + derr.Error()
			overall = "degraded"
			items = append(items, item)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		item["http_status"] = res.StatusCode
		if res.StatusCode >= 200 && res.StatusCode < 400 {
			item["status"] = "reachable"
		} else {
			// 4xx/5xx:域名解析并应答了,但健康面未达成——如实降级。
			item["status"] = "unreachable"
			overall = "degraded"
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     overall,
		"service":    "touch-server",
		"checked_at": domainHealthRFC3339(time.Now()),
		"targets":    items,
	})
}
