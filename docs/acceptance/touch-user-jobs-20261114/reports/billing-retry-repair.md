# HUI-4664 原订单恢复与硬件订单事务修复

分支 `codex/touch-billing-retry-repair`，工作树 `.worktrees/billing-repair`，起点 `916bbf0`。独占变更仅 `store_billing.go`、`handlers_billing.go` 及对应两个测试文件；本地提交，不 push、不写 Linear、不标 Done。没有操作 commercial 原数据库，也未改支付/开通事件权威、前端、cashier 或 Identity 替身。

## 真实红证据

先新增回归、运行原实现：

- `TestBillingOrderRejectsChangedIntent`：相同 tenant/key 改 actor、role、payer、payer grant、agency relation、action、plan、PriceRef、金额、币种、reason，原方法全部返回 nil/replay，未核验原请求。
- `TestHardwareOrderAtomicAndItemIntent`：两条明细各自合法，但第二条与第一条的内部主键相同，真实 SQL 第二次 insert 失败；旧方法留下已提交 order 与第一条 line（测试先因 `ListBillingOrders` 非空而红）。这模拟明细落库故障，并非仅做输入校验。
- `TestSubscriptionOriginalOrderRecoveryAfterCatalogChangeHTTP`：真实 HTTP 首单201后，目录 PriceRef 更新，同 body 同 key 重投旧实现409 `catalog_changed`，预期200恢复原单。
- `TestHardwareSameTotalDifferentItemsConflictHTTP`：真实 HTTP 同 key、同总价、换 SKU，旧实现200 replay，预期409 `idempotency_conflict`。

初次红测试命令：`go test ./internal/store -run 'TestBillingOrderRejectsChangedIntent|TestHardwareOrderAtomicAndItemIntent' -count=1` 与 `go test ./internal/httpapi -run 'TestSubscriptionOriginalOrderRecoveryAfterCatalogChangeHTTP|TestHardwareSameTotalDifferentItemsConflictHTTP' -count=1`。均真实失败后才实施。

## 实际修复

1. `CreateBillingOrder` 收口原 `billing_orders` 事务。先 `INSERT ... ON CONFLICT(tenant_id,idempotency_key) DO NOTHING` 取得写锁，再读取获胜原单；重放严格比较 kind/action/plan/PriceRef/原价格快照/金额/币种及 actor principal/role、payer principal、payer grant ref、agency relation ref。JSON 快照忽略对象字段顺序与空白；created_at、实时订单状态与支付事实不参与请求意图，不改历史状态。
2. HTTP 订阅下单先沿原 live payer 资格与服务端 payer 解析授权，再按 tenant/key 恢复原订单。原价格快照增加规范化 `creation_request`（动作、套餐、原price_ref、加店数、原因、原幂等key），无迁移与第二权威。相同请求重投直接使用原价/金额/快照，不重新访问当前目录。当前目录变价、失效或没有生效行，都不妨碍恢复已成立原单。
3. 目录/创建冲突路径也重查原 key，覆盖另一重试在初次查询后刚提交同意图原单的竞争；仍先核请求语义与当前 actor/payer，不能把异语义冲突吞成成功。新 key 继续走当前目录，旧 PriceRef 在现行价已变时仍409 `catalog_changed`。
4. 历史无 `creation_request` 的原单依原 snapshot 已记录的 action/plan/price/additional_stores/reason 比较，并沿原 actor/payer/grant/relation核验。历史原调用未提供 PriceRef 时允许接受原冻结价；不伪造历史未存的请求字段。
5. `CreateHardwareOrder` 在同一事务内创建原 order 与全部 item lines；任何明细 insert 失败 rollback 全部。重试读原明细，比较去内部ID/OrderID后、按完整值排序的明细多重集（SKU/name/kind/qty/unit amount）；换顺序可恢复，换SKU/品名/数量/商品服务/单价或行数量会冲突。校验明细合计与订单总价一致，拒绝乘法/加法溢出。
6. 商家创建端点统一将原 `ErrBillingIdempotencyConflict` 返回409 `idempotency_conflict`；受信 authority 事件端点原 `IDEMPOTENCY_CONFLICT` 词汇与状态机不改。下单依然只产生 processing 投影，不表示已付款、已开通或实体交付。

## 验证结果

- 初步 focused store/httpapi 红→绿；后续 `go test ./internal/store ./internal/httpapi ./internal/billing -run 'Billing|Hardware|Subscription|AgentRenewalOrder' -count=1` 通过（1.055s/2.193s/0.002s）。
- 新增真实 HTTP 代理矩阵：另一个仍有付款权限的 actor 不能复用原 key；相同代理+相同live grant恢复200；替换payer grant后原key409；撤回payer授权后原key403，不能借恢复绕过撤权。
- 目录变价/失效通过原受信 `/api/v1/billing/authority/catalog` 工程缝写入，原key恢复保留原amount，newkey旧PriceRef409；无当前目录的历史扩店原单可恢复，加店数改变409。
- store 8路同key硬件并发，只创建1单和1套完整明细；模拟line2落库故障后order与lines均零，原key可正常重试；明细重排仍返回原id，等额换SKU409。13类原订单意图变更拒绝。
- 最终恢复竞争收口后的相关选测通过：httpapi2.493s、store1.377s；历史原单HTTP测试单独通过0.175s。
- 全 `go test ./internal/httpapi ./internal/store ./internal/billing -count=1` 首轮通过（108.289s/68.743s/0.005s）；最终精确代码另跑完整日志在 `runtime/billing-retry-suite.log`，结果与本地commit见补记。
- 无前端变更，因此未重跑前端构建；`git diff --check` 通过。

## 独立浏览器复验所需最小前提

正常路径为 `/` 登录→本商家 `/work/billing`→原报价→确认原订单→原订单回查。需有效 owner；代理需原激活关系、renewal 与非空 payer grant，过期/撤销必须即时拒绝。同商家目录应由真实受信 `billing_authority` 生产者通过原 `POST /api/v1/billing/authority/catalog` 发布（合法套餐、PriceRef、pricing_version、币种、金额、服务期、门店额度和生效窗口）；不通过 SQL 插价或假支付构造正式成功。工程HTTP测试使用明确标识的目录fixture，测试价格不代表生产定价。

现有 `commercial-ops-restart.mjs` / `commercial-followup-env.mjs` 已注册工程 `billing-auth` 的 `billing_authority` 服务角色及原受信端口，但脚本不种订阅目录，也不设置 `TOUCH_CASHIER_APP_ID`、`TOUCH_CASHIER_TARGET_ID`。现有 `identity-stub.mjs` 的 shell launch 仅支持 goboost/aicut/dhuman 三个目标，没有 cashier target；现有 seams stub 只覆盖内容制作quote/task，不能当作订阅收银台。因此现harness可核报价/原单HTTP恢复及缺收费通道诚实态，不能直接宣称正常浏览器订阅下单/收银已可用。当前账单 UI 的订单提交守卫要求 `checkout.available`。

若要沿正常产品UI走到订单正向，需要已登记的收银台 app/target 与受信 Identity shell launch；两项环境配置须对应真实登记，不借用其他应用target，也不能把随意配置available视为收银可用。本批不新增支付模拟，不把工程目录、订单processing或回查标为正式付款。生产Identity、真实收银支付、服务开通、实卡回读与真机验收仍属于外部联调与正式交付，未验证。

## 最终提交补记

本地 commit：`81f6668de81dec99007ff37d79dbacae2e8caf78`（`fix: recover original billing intent and atomically save hardware orders`）；工作树干净，未push。

最终完整命令 `go test ./internal/httpapi ./internal/store ./internal/billing -count=1` 通过：HTTP93.737s、store60.158s、billing0.009s。实际stdout/stderr完整落盘：`.superpowers/sdd/touch-user-jobs-20261114/runtime/billing-retry-suite.log`。执行期间未修改运行行为，之后只修正helper注释归属。`git diff --check` 通过。
