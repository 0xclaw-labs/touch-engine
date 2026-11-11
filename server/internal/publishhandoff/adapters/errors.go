// errors.go: 平台适配器的顾客可恢复错误哨兵(HUI-4587 窄补丁,httpapi
// writeActionHandoffError 增加一行映射)。
//
// 语义:提交前的顾客侧前置条件不满足(未授权/授权过期/受控引用过期/平台调用
// in-flight 未知)——不是适配器能力缺位(ErrUnsupported),也不是服务器内部
// 错误;顾客回到交接页重试/重新授权即可恢复,状态机不推进、不重投。
package adapters

import "errors"

// ErrCustomerActionRequired marks a recoverable customer-side precondition
// gap on the submit/OAuth path (顾客可恢复:回交接页重新授权或重取素材引用).
var ErrCustomerActionRequired = errors.New("adapters: customer action required to proceed")
