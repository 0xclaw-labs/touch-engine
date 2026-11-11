// platform_steps.go: 4588 手动家族逐平台操作说明(准确官方入口/步骤;建议
// 补充平台保留标注)。这里只有"怎么做"的说明文字——能力判定(能不能/准入)
// 真源在 3535 记录,受控端点更新;本文件永不改判定。
package adapters

// manualStepsFor returns the honest instruction set per platform
// (A04/A05/A06 必需;A22/A24/A25/A27/A28 建议补充标注保留).
func manualStepsFor(platform string) ManualPlatformSteps {
	switch platform {
	case "wechat":
		return ManualPlatformSteps{
			TargetName: "微信",
			Steps: []string{
				"把下方素材保存到手机相册（视频/多图按给出顺序保存）",
				"打开微信朋友圈，从相册按顺序选择刚保存的素材",
				"粘贴你主动复制的参考文案后，自行完成编辑与发布",
				"微信没有可核实的普通 H5 原生发布回执：发布与否以你的实际操作为准，系统不假判成功",
			},
			LinkSteps: []string{
				"复制或分享下方门店链接到微信",
				"由接收方自行打开链接查看；链接分享与原生发布是两种不同能力",
			},
			TargetHint: "回到微信，在朋友圈发布页按顺序选择素材",
			MediaNote:  "素材通过短时效受控链接提供；请保存到手机相册。下载落「文件」时请在文件管理中手动转存相册。链接过期可在交接页重试获取，不会新建交接。",
			NotePrefix: "微信朋友圈/视频号普通 H5 原生发布契约未核实：本路线只做素材交接与操作指引，不自动发布。",
		}
	case "wechat_channels":
		return ManualPlatformSteps{
			TargetName: "微信视频号",
			Steps: []string{
				"保存下方视频与封面素材到手机相册",
				"打开微信视频号发布页，选择刚保存的视频",
				"粘贴你主动复制的参考文案后，自行完成编辑与发布",
				"视频号原生发布无已核实回执：结果以你的实际操作为准，系统不假判成功",
			},
			LinkSteps: []string{
				"复制或分享下方门店链接",
				"视频号卡片分享与素材发布是不同能力；由接收方自行打开查看",
			},
			TargetHint: "回到微信，在「视频号」发布页选择素材继续",
			MediaNote:  "视频与封面分开保存；短时效受控链接过期可在交接页重试获取。",
			NotePrefix: "微信视频号素材交接与操作指引；不自动发布、不伪装原生发布器。",
		}
	case "bilibili":
		return ManualPlatformSteps{
			TargetName: "B站",
			Steps: []string{
				"保存下方素材到手机",
				"打开 B 站投稿页（App 内「发布」），选择刚保存的素材",
				"粘贴你主动复制的参考文案后，自行完成编辑与投稿",
			},
			LinkSteps: []string{
				"复制或分享下方门店链接",
				"B站开放能力不等于免授权移动 H5 发布器；由接收方自行打开链接",
			},
			TargetHint: "回到 B 站 App 投稿页继续",
			MediaNote:  "素材短时效受控链接；请及时保存到本地。",
			NotePrefix: "【建议补充】B站视频交接（本动作同版建议，非获批自动发布）。",
		}
	case "weibo":
		return ManualPlatformSteps{
			TargetName: "微博",
			Steps: []string{
				"保存下方素材（图文按顺序/视频+封面）到手机相册",
				"打开微博发布页，选择刚保存的素材",
				"粘贴你主动复制的参考文案后，自行完成编辑与发布",
			},
			LinkSteps: []string{
				"复制或分享下方门店链接到微博",
				"微博开放能力不等于免授权移动 H5 发布器；链接分享由接收方自行打开",
			},
			TargetHint: "回到微博发布页继续",
			MediaNote:  "素材短时效受控链接；多图请按顺序保存。",
			NotePrefix: "【建议补充】微博素材交接（本动作同版建议，非获批自动发布）。",
		}
	case "qq":
		return ManualPlatformSteps{
			TargetName: "QQ",
			Steps: []string{
				"由你选择 QQ 好友/群作为分享目标（系统不会自动发送给任何联系人）",
				"复制或通过系统分享面板交出下方门店链接",
				"分享结果以 QQ 内实际操作为准；只交出链接不等于发布成功",
			},
			LinkSteps: []string{
				"复制下方门店链接（或使用系统分享面板选择 QQ 目标）",
				"由你本人选择好友/群后发送；系统不会代发消息",
			},
			TargetHint: "回到 QQ 选择分享目标",
			MediaNote:  "纯链接分享无内容包依赖；素材分享仅在已明确选择合法素材时可用。",
			NotePrefix: "【建议补充】QQ 好友/群链接分享（默认分享公开门店链接，由用户选择目标，不自动发消息）。",
		}
	case "qzone":
		return ManualPlatformSteps{
			TargetName: "QQ空间",
			Steps: []string{
				"保存下方素材到手机相册（多图按顺序）",
				"打开 QQ空间发布页，选择刚保存的素材",
				"粘贴你主动复制的参考文案后，自行完成发布",
			},
			LinkSteps: []string{
				"复制或分享下方门店链接到 QQ空间",
				"链接分享由接收方自行打开；与素材发布分开判定",
			},
			TargetHint: "回到 QQ空间发布页继续",
			MediaNote:  "素材短时效受控链接；请及时保存到本地。",
			NotePrefix: "【建议补充】QQ空间图文交接（本动作同版建议，非获批自动发布）。",
		}
	default:
		return ManualPlatformSteps{
			TargetName: platform,
			Steps: []string{
				"保存下方素材到手机",
				"打开对应平台发布页选择素材",
				"粘贴你主动复制的参考文案后，自行完成发布",
			},
			LinkSteps:  []string{"复制或分享下方链接", "由接收方自行打开链接"},
			TargetHint: "回到目标平台继续发布",
			MediaNote:  "素材短时效受控链接；过期可在交接页重试获取。",
			NotePrefix: "手动素材交接：只做预览/保存/复制/系统分享与操作指引。",
		}
	}
}
