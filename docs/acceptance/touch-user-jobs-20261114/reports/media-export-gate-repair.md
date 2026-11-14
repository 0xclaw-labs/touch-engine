# 公共媒体导出准入修复

- 日期：2026-11-14；基线 `a69a3a0`。
- 独立树 `.worktrees/media-export-repair`，分支 `codex/touch-media-export-gate`；本地提交：`c40055968773a51a891a21e4284b7b9ed3376c62`。
- 承接原独立审查的真实 A01/C2 旁路：h5_share、manual_material 均 not_admitted，却可从公共 media-access 签 download 并下载 tiny.mp4。本修复仅处理后端签发、兑换及当前执行投影，不用隐藏按钮替代准入，不修改前端、不更改原浏览器 DB、Linear 或远端。

## 根因

原公共 media-access 只验版本可分发、已发布/本人候选及媒体属于版本，没有把导出与原动作方法/场景/环境准入、原媒体 handoff 绑定。空 session 也能签非预览票据。兑换只验签、TTL、版本分发及文件，没有原来源的当前权限。旧 merchant、legacy 与漏洞 public 都可能产生相同的空 session claim，审计也不能区分其合法来源，不能靠空值维持自动放行。

`FindActionForSession` 会走 Refresh；但 Refresh 的 nil adapter 直接返回原行，不执行 live gate。因此读取旧冻结计划或调用 Find 不能代表当前获准。

## 实施

1. 公共 `download` / `platform_fetch` 必须有本人 active session 和原 media handoff（可显式给 handoff_id；未给时只解析本人该会话、精确冻结版本的原行）。原动作、平台、方法、场景、环境沿既有 `resolveActionCapability` + `JudgeActionPlan` 当前裁决；当前动作与内容分发状态仍是独立否决。
2. 校验同租户、同门店、同动作、同会话修订、同冻结版本与原快照内媒体；不能以 version、任意 session、候选池或错 handoff 升级导出。manual_only 仅 download；官方媒体路线仅 platform_fetch，不增加旁路 download。
3. 现有 claim 增加受原 HMAC 签名保护的 `source_kind/source_ref`，由服务端写入，公共 body 不接收。保留 `sr` 的原 action-session 语义，不重载为其他身份。
4. 动作来源绑定原 handoff；adapter 开始阶段的引用绑定原租户/会话/版本下唯一幂等请求，实际行在 Prepare 前已落定；刷新绑定 handoff ID。只读 `LoadMediaSource` 不刷新 adapter、不写事实；来源仍必须经过完整验证器，不能单独作为授权。
5. 商家素材管理继续沿既有 scoped wrapper 签发，来源为当前 member ID；兑换重查 enabled、tenant、store scope 和原 ActionReadList 权限。legacy 内部 mint 仅增加原活动 handoff 来源，兑换核原参与、handed_off attempt、活动归属、冻结版本及原路线，不新建会话、参与或交接。
6. 非预览的签发、兑换及内部 mintActionMediaRefs 都独立复核当前来源和准入；平台兑换不要求访客/商家 cookie。HEAD、Range及缓存 token 也经过相同否决。预览仍可取得实际字节，不声称防拷贝。
7. v2 start/get/session-handoff 新增当前 `execution` 投影。历史 GET 仍200并保留原 handoff/status；拒绝时 execution.allowed=false，plan.allowed=false、plan.kind=refused/pending_external及准确reason/reason_text；同时移除 handoff.client_action、plan.client_action，media为空。原DB历史计划和状态不重写。终态/在途原行只读，不新Prepare；幂等start仍先走当前gate。手动计划不嵌入适配器专用platform_fetch链接。

## 精确兼容与返回码

| 场景 | 返回 |
|---|---|
| 公共非预览，无本人原合法媒体交接 | 422 media_handoff_required |
| 已准入 session 但尚未开原 handoff | 422 media_handoff_required |
| 原交接用途不匹配（如官方抓取改download） | 422 media_purpose_not_allowed |
| 方法/场景当前准入拒绝或未知 | 422 handoff_refused（reason按原准入权威） |
| 跨访客/租户/冻结版本、错来源 | 404 not_found |
| scoped商家成员撤权后的缓存兑换 | 403（原授权reason） |
| 动作停用后的签发/缓存兑换 | 404 not_found |
| 内容停止分发/授权到期 | 409（原内容权威reason） |
| 取消/失败来源 | 409 invalid_state |
| 旧票据非预览且无可证明source、未知source | 410 media_source_renewal_required |
| 旧preview票据及正常公共preview | 200，真实bytes合同保留 |
| v2历史GET当前不可执行 | 200原历史 + execution.allowed=false，清执行载荷 |

旧无来源非预览票据不能区分来源，明确失效，要求从原已准入 handoff 或 scoped 商家素材管理重签；默认10分钟/最长1小时TTL自然淘汰，无新迁移/新表。不能推测旧空sid是商家或平台。真实外部平台必须使用原来源重签后的抓取URL；不要求平台保存访客cookie。

## Red → Green

- 先写 `TestPublicMediaExportCannotBypassNotAdmittedRoute`，纠正夹具的缺失公网目标后得到真实业务红：A01无会话/本人会话的download、platform_fetch均200，预期422；preview保持200且实际读取文件。生产代码尚未修改。
- `TestOldMediaExportWithoutVerifiableSourceRequiresRenewal`：旧空来源download/platform_fetch原兑换200，预期410；旧preview保持200。
- `TestMerchantMediaExportKeepsScopedAuthority`：商家原合法下载可取真实文件，但成员撤权后的缓存token仍200，预期403。
- 上述修后转绿；使用实际seeded A02 admitted manual_material，经真实商家上传tiny.mp4 → 建版本并采用 → 本人session → 原计划指纹确认 → 原handoff → 无cookie下载attachment。已准入session无handoff仍422；原manual不能升级platform_fetch；撤该方法后签发和缓存兑换422。
- 新增niladapter旧历史投影红：撤准入后GET仍plan.allowed=true并返回旧client_action/URL。修后GET保原status，但execution/plan明确拒绝，两处client_action均不存在，media为空；旧幂等键重放仍被not_admitted422阻止。
- 官方抓取工程合同夹具（显式tenant capability fixture，不代表真实平台获批）验证原action handoff与原adapter开始幂等来源都可无cookie取得真实bytes，官方路线额外download422。
- 动作停用、内容撤权、跨visitor/tenant/version拒绝；同sha文件在不同租户不等于授权；claim来源变更不能保留有效签名。仅隔离测试库包含固定事实，不动原浏览器库。
- 原 `TestCampaignContentContractHandoffRealBytesAndFrozenParticipation` 等legacy回归仍从原活动/参与/冻结attempt取得tiny.mp4及图片，已有内容换采用不覆盖冻结引用、内容撤权拒绝新签发/缓存兑换的断言保留。
- 原两条泛媒体测试原先直接公开签非预览；改为公开preview验证受控URL、商家合法下载验证attachment，保留真实MIME/HEAD/Range/ETag/过期/篡改断言，旁路反例由新测试明确锁住。

## 实际验证

环境 `source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh`，Go 1.26.5；未提权、未下载浏览器。

- 媒体/动作v2/legacy/活动内容合同相关定向 suite：通过，12.152s（包含current execution投影）。
- 最后补验manual旧幂等键与official原adapter来源：通过，0.460s。
- 前一轮 `go test ./internal/httpapi ./internal/contentpack ./internal/publishhandoff -count=1` 全相关包通过（124.889s/0.004s/8.205s）；该轮在最终execution投影之前。最终完整相关包重跑同样通过：**httpapi 120.834s / contentpack 0.003s / publishhandoff 8.814s**；末尾补充幂等重放与原adapter来源测试再定向通过。
- 提交前 `git diff --check` 通过；9个文件仅本后端写面、Go测试与合同说明，提交后独立树干净，无前端混入。

## 未冒充的证据

本次为独立工程测试及固定事实复算；preview实际字节可被浏览器读取，不承诺防保存或防拷贝。官方路线工程夹具不证明该主体当前具备真实平台资格、真实SDK签名、投稿、支付或官方回执。正常浏览器原A01/C2旁路及可用manual路线由root合前端后交Terra沿原路径复验；未自行把任何票标Done。


## 提交文件清单

- `server/internal/httpapi/handlers_public_action_content.go`：签发/兑换共享的只读来源、会话、准入及用途验证。
- `server/internal/httpapi/handlers_action_handoffs.go`：原内部mint来源及当前execution投影；manual不嵌入adapter专用URL。
- `server/internal/httpapi/campaign_content_media.go`：legacy内部mint仅增加一行签名来源绑定。
- `server/internal/contentpack/media_access.go`：两个受签名保护的可选来源字段及来源种类，不改原session字段。
- `server/internal/publishhandoff/service2.go`：仅新增只读LoadMediaSource，不改状态机/业务写入。
- `server/internal/httpapi/server_media_export_gate_test.go`：固定事实、真实上传字节、正反准入/来源/撤权/历史投影回归。
- `server/internal/httpapi/server_public_action_content_test.go`、`server/internal/contentpack/media_access_test.go`：保留原合法媒体与签名合同，改正原公开非预览绕过预期。
- `docs/contracts/publish-handoff/v2/handoff-contract.md`：当前执行、来源、旧票据失效与返回码合同。


## 后续窄修：手动交接浏览器文件参数

root复查发现实际消费接线：浏览器仅从 `client_action.params.media_order/media_NN` 预取 File，不读取顶层 `media[]`。manual-only start此前正确停止给adapter传platform_fetch，但没有把本次download引用投影回这两处参数，合法取材因而显示“本次没有可交接的文件”；刷新仍残留adapter专用抓取参数。

- 后续提交：`24413681c47b00577a6460b42c88695ff070c5d9`，在 `c400559` 后，仅两个文件：`handlers_action_handoffs.go` 和 `server_media_export_gate_test.go`。
- 只在当前execution允许、原plan_kind=manual_only、client_action.type=manual_export时，克隆当前client_action参数；清除旧media_*，按同次已合法签发的download media[]（其顺序源于原冻结快照）生成media_order及media_NN=kind:url，同时用于handoff和plan两处client_action。
- 保留caption、平台步骤、note、unsupported等原信息及其他非媒体参数；不改持久快照、原client_action，不调用额外平台动作、不引入另一媒体来源。被拒时仍没有client_action或新media。
- Red：真实注册wechat ManualAdapter的start缺少media_order，refresh参数不等于同响应download；无adapter的允许manual计划start无文件参数、refresh残留旧media_99/过期platform_fetch，均先跑红。
- Green：注册wechat/A04及无adapter/A02两组正常start与refresh中，两处client_action的每个URL严格等于同响应download媒体，顺序与冻结快照相同；无访客cookie GET验证真实MIME及字节SHA256；无adapter夹具确认旧持久参数未被响应投影改写；撤准入后两处client_action与media都清除。
- 验证：`go test ./internal/httpapi -run 'TestManualResponseProjectsCurrentDownloadFilesForBrowser|TestManualMediaExportRequiresOriginalHandoffAndLiveAdmission|TestOfficialMediaFetchCannotBecomeGeneralDownload|TestActionHandoffV2MediaHappyPath' -count=1` 通过（1.048s）；提交前diffcheck通过，提交后树干净。按root要求此窄修不再重复全包，最终集成全包由root执行。
