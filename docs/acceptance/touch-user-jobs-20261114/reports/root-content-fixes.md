# Root 内容正常流程修复

实施候选：23931d8、e6ebca1、a86747b；集成映射44bb519、a3cd566、1fb68f6。后续同版候选05bf121包含店员和顾客集成，待独立Terra。

## 正常入口已发现并修复

- 首页→门店→添加内容，真实B05 tiny.mp4和图片文件可上传，但普通介绍被price_note/valid_note强制必填阻挡。现为可选展示备注，素材grant/活动奖励的真实期限不变，不补造免费/永久有效。
- 增加图文形式和有序图片选择、移动、预览和逐文件下载，沿原lib_assets权威；补齐原支持平台选项，不代表取得发布方法资格。
- 当前组织/门店/活动用途的编辑内容与选择恢复，同名跨用途不会偷偷复用另一活动内容包；沿原唯一约束生成可读数字后缀。
- 付款安排显示真实组织配置上下文，原account标识仅在核对详情；账户名称上游未给，不伪造。制作配置仅表示可获取报价，不宣称已受理/交付。
- store_manager本店制作记录/能力读取原403误拒，先红后绿改为受授权门店读权限。报价仍独立检查付款权，经理无付款权403，越店403。
- 真零报价与缺金额区分：wire sourceQuote/sourceHold金额用pointer，缺字段拒绝，明确0允许；负值拒绝。原hold恢复同一记录，不开第二笔。
- 提交前若浏览器无法持久保存原请求，明确本次未提交并恢复按钮，不让异常卡在busy；成功后的列表刷新失败不抹去已登记事实。制作付款/过期/预占同步错误给出原请求恢复方向。

## 真实公共合同核对

GitHub连接器读取0xclaw-labs/public-ai主干文件（私库未绕过shell认证）：

- internal/billing/unified_http.go，blob b183b87b5f9745907a8f8e062590800a2ab53599：原usage查询必须携app/payer_account/payer_user scope，返回facts。
- internal/billing/unified_reservation.go，blob 6c224df47e21aa24ffe930fbc9bc6fb8eb3ff462：source_reservation_v1明确允许expected_amount_minor=0，nil/负数拒绝；quote_id与金额绑定，原幂等hold回放。
- internal/billing/unified_usage.go，blob 793b4d27a422463ab1569dc1864c9c73f9f1ae25：同usage已报价返回原固定快照，不重定价。故不另造重报价/二次收费协议。

## 证据

- root前轮真实Chrome正常/admin登录→首页→门店→内容制作：仅报价usage1/quote1/hold0/charge0/task0；明确确认后hold1/task1/charge0，刷新恢复同任务。环境是工程fixture，非真实外部交付。
- 图文真实HTTP上传、无可选备注候选与采用通过；purpose helper/domain先红后绿。
- 本店经理读能力/记录先403红，修后200，越店403且无付款授权报价403，相关HTTP回归绿。
- 真实零价用例先红，a86747b后production/httpapi指定用例绿（含明确零价、缺quote金额拒绝、缺hold金额拒绝、旧原hold恢复不新POST、普通内容无备注）。日志logs/root-content-zero-go.log。
- web两文件20项与tsc绿，日志logs/root-content-zero-web-final.log。此前staff全库发现的三条旧文案与Go缺price旧预期已更新为新合法业务含义，最终合版仍须跑全库。
- 未宣称制作已交付或真实支付完成；Terra正常浏览器与对抗复验尚待执行。
