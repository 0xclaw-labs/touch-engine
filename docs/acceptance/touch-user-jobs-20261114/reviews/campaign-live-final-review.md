# 活动草稿、线上与预留独立复审

候选 `e2cfe3aedcb53ed787ac8a1d9f66e5ba40c49371`，独立审查树原 `1ca6647` 干净后快进；Next 生产构建成功。原 `/tmp/touch-e2e-ZLFKmv/touch.db` 和 media，活动 `cmp_6f20f5350102a4c00e08b5ffd22abb01`，浏览器与全新 Go/Next/Identity/seams 同一执行生命周期。原 cookie 未继承，正常 `/admin` 登录→首页→推广活动→「复核到店优惠」。工程替身不是正式身份或平台。

## 原记录正常路径

原活动有活动介绍页、无已启用推广承诺。选原采用内容，填领取/使用时间、数量10和顾客说明，正常保存后界面显示「编辑稿第1版 · 尚未启用」；原公开 `/c/HDR2727ZSH71` 显示活动开放但推广优惠尚未启用，未展示刚保存的优惠承诺或领取按钮。点击「启用第1版草稿」被拒，明确至少需要一条准入有效渠道；原渠道均未获准，没有绕开或伪造。截图 `runtime/campaign-final-draft.png`。这不是「线上v1保存v2仍保留」的浏览器正例：原库没有合法 enabled 承诺与真实渠道资格，不能伪造。

原店从工作区「添加服务」可见完整 A01–A21 必需和 A22–A30 九项补充目录，A27 已有条目；截图 `runtime/campaign-final-catalog.png`。这些是30项目录，并不证明28个业务视图逐一可达或真实平台执行；本轮未完成28视图逐项映射。

## 独立工程对抗

审读 campaign-live 变更的存储、HTTP、前端与新增测试：正式读取以 enabled_version 指向 enable/resume 原修订，校验 spec/完整 disclosure；草稿保存保留 pin，显式启用 CAS，恢复原版；ledger 新 reserve/confirm 在原事务内核对配置与活动状态，原已发事实回放和核销保留。独立运行 `go test ./internal/httpapi ./internal/store -run 'TestCampaignLiveDraft|TestPublishedConfig|TestPublishedRewardPauseCommit' -count=1`，HTTP 和 store 均通过。隔离 fixture 涵盖缺失/非法修订 fail closed、v1发布/v2草稿、显式启用、暂停保存及恢复原版、确认先后顺序、已发券核销、全 reserved 恢复与拒新参与；测试中受信接口建立原 reservation，并非普通参与端到端已连接。

**阻断：原预留接线缺失。** `server.New` 给 participation 注入 nil BenefitReserver，`participation.Service.Start` 仍创建参与并记 not_requested，不申请预留；活动配置启用成功提示却承诺顾客开始前申请、未预留成功不会白跑。该不一致已报 root 接真实原权威，不能仅改文案或虚构预留；需修后复验正常参与、竞争和失败恢复。

平台未获准，原真实线上版保存、暂停/恢复、旧参与/券和正式渠道发布无法在原活动浏览器正向证明；隔离测试不能代替这些条件。真实平台、支付制作、实体卡及真机仍未证，不标 Done。
