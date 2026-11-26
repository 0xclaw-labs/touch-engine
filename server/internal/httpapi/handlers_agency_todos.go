// handlers_agency_todos.go: HUI-4664 A1 代理真实授权商户待办投影。
//
// 纪律:
//   - 零示例/演示任务(ZERO sample/demo tasks):所有待办全部来源于当前真实激活、
//     未过期、成员行启用的代管关系下的真实业务行;无授权商户即返回空列表;
//   - 严格以服务端解析的 agent_principal 为唯一主体收口;已撤销/已过期关系
//     立即不可见,绝不暴露跨商户或越权数据;
//   - 每条待办必带对象(object_ref/object_label)、影响(impact)、状态(status)、
//     所属域(domain)、责任人(owner)、当前代理是否可办(can_actor_handle)与
//     受控去向(target_path);
//   - 明确区分配置权限与独立付款授权(payer_granted):无独立 payer 权限的代理
//     仍可办理获准的非收费配置事项,收费待办明确标注交回商家负责人。
package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

type agencyTodoItem struct {
	ID             string `json:"id"`
	Category       string `json:"category"` // pending_onboarding | missing_config | hardware_acceptance | renewal | exception
	TenantID       string `json:"tenant_id"`
	TenantName     string `json:"tenant_name"`
	RelationID     string `json:"relation_id"`
	ObjectRef      string `json:"object_ref"`
	ObjectLabel    string `json:"object_label"`
	Impact         string `json:"impact"`
	Status         string `json:"status"`
	Domain         string `json:"domain"`
	Owner          string `json:"owner"` // agent | merchant | platform
	CanActorHandle bool   `json:"can_actor_handle"`
	TargetPath     string `json:"target_path"`
	NextStep       string `json:"next_step"`
}

func hasAgencyAction(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// handleAgencyTodos answers GET /api/v1/agency/todos (A1 真实待办投影)。
func (s *Server) handleAgencyTodos(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
		return
	}
	rels, err := s.St.ListActiveAgencyRelationsByAgent(c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "agency relation list failed")
		return
	}

	nowTime := time.Now().UTC()
	nowStr := nowTime.Format(time.RFC3339)
	merchants := make([]map[string]any, 0, len(rels))
	todos := make([]agencyTodoItem, 0)

	for _, rel := range rels {
		member, merr := s.St.GetMemberByPrincipal(rel.TenantID, c.Member.PrincipalRef)
		if merr != nil || !member.Enabled || member.Role != string(authz.RoleAgent) ||
			!s.St.HasValidAgencyRelation(rel.TenantID, c.Member.PrincipalRef, nowTime) {
			continue
		}
		tenantName := "已授权商家"
		if t, terr := s.St.GetTenant(rel.TenantID); terr == nil && t.Name != "" {
			tenantName = t.Name
		}
		canRenew := hasAgencyAction(rel.AllowedActions, authz.AgencyActionRenewal)
		canPay := canRenew && rel.PayerGrantRef != ""
		canConfigStore := hasAgencyAction(rel.AllowedActions, authz.AgencyActionStorefront) ||
			hasAgencyAction(rel.AllowedActions, authz.AgencyActionAction) ||
			hasAgencyAction(rel.AllowedActions, authz.AgencyActionChannelTarget)

		merchantTodos := make([]agencyTodoItem, 0)

		// (1) 门店与配置缺失检查(代理具备门店只读能力;具体修改权限以授权动作为准)。
		stores, serr := s.St.ListStores(rel.TenantID)
		if serr == nil {
			liveStores := 0
			for _, st := range stores {
				if st.Status == store.StoreStatusActive {
					liveStores++
				}
			}
			if liveStores == 0 {
				owner := "merchant"
				if canConfigStore {
					owner = "agent"
				}
				merchantTodos = append(merchantTodos, agencyTodoItem{
					ID:             fmt.Sprintf("todo:config:nostore:%s", rel.TenantID),
					Category:       "missing_config",
					TenantID:       rel.TenantID,
					TenantName:     tenantName,
					RelationID:     rel.ID,
					ObjectRef:      rel.TenantID,
					ObjectLabel:    tenantName + " · 门店",
					Impact:         "尚未配置启用中的门店，顾客侧门店页与投放入口无法对外承接。",
					Status:         "missing_store",
					Domain:         "store_config",
					Owner:          owner,
					CanActorHandle: canConfigStore,
					TargetPath:     "/work/stores",
					NextStep:       "进入商户工作台补充门店基础资料与服务配置。",
				})
			}
		}

		// (2) 订阅开通/续费、待处理订单与异常(仅当具备 renewal 授权时可见账单投影)。
		if canRenew {
			fact, effective := s.billingSubscriptionFact(rel.TenantID)
			orders, oerr := s.St.ListBillingOrders(rel.TenantID, "")
			refunds, _ := s.St.ListBillingRefunds(rel.TenantID)
			refundsByOrder := map[string][]store.BillingRefundRow{}
			for _, rf := range refunds {
				refundsByOrder[rf.OrderID] = append(refundsByOrder[rf.OrderID], rf)
			}
			deliveries, derr := s.St.ListTouchpointDeliveryViews(rel.TenantID, store.TouchpointDeliveryFilter{})
			deliveriesByOrder := map[string][]store.TouchpointDeliveryView{}
			if derr == nil {
				for _, dv := range deliveries {
					deliveriesByOrder[dv.HardwareOrderID] = append(deliveriesByOrder[dv.HardwareOrderID], dv)
				}
			}

			hasActivePendingOrder := false
			if oerr == nil {
				for _, o := range orders {
					var fPtr *billing.HardwareFulfillment
					if o.Kind == billing.KindHardware {
						if f, ferr := s.St.GetHardwareFulfillment(rel.TenantID, o.ID); ferr == nil {
							fPtr = &f
						}
					}
					cont := billing.BuildOrderContinuity(billing.OrderContinuityInput{
						Order: billing.OrderState{
							ID: o.ID, TenantID: o.TenantID, Kind: o.Kind, Action: o.Action,
							PlanCode: o.PlanCode, PriceRef: o.PriceRef, PriceSnapshot: o.PriceSnapshot,
							AmountMinor: o.AmountMinor, Currency: o.Currency, Status: o.Status,
							OrderRef: o.OrderRef, PaymentRef: o.PaymentRef, FailReason: o.FailReason,
						},
						CreatedAt:          o.CreatedAt,
						UpdatedAt:          o.UpdatedAt,
						CheckoutAvailable:  s.billingCheckoutSeamReady(),
						ActorCanPay:        canPay && o.PayerPrincipal == c.Member.PrincipalRef && o.PayerGrantRef == rel.PayerGrantRef,
						ActorIsAgent:       true,
						Fulfillment:        fPtr,
						PhysicalDeliveries: deliveryViewsToFacts(deliveriesByOrder[o.ID]),
						Refunds:            refundRowsToFacts(refundsByOrder[o.ID]),
					})
					switch cont.LifecycleStage {
					case billing.StagePendingPayment:
						hasActivePendingOrder = true
						actorHandles := canPay && cont.NextStep.AllowRetryPayment
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:order:" + o.ID,
							Category:       "pending_onboarding",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      o.ID,
							ObjectLabel:    fmt.Sprintf("%s · 原单 %s", tenantName, o.ID),
							Impact:         "原单等待支付，服务或硬件尚未生效。",
							Status:         cont.LifecycleStage,
							Domain:         cont.NextStep.Domain,
							Owner:          cont.NextStep.Owner,
							CanActorHandle: actorHandles,
							TargetPath:     "/work/billing",
							NextStep:       cont.NextStep.Summary,
						})
					case billing.StageServiceActivating, billing.StagePaymentPaid:
						hasActivePendingOrder = true
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:order:" + o.ID,
							Category:       "pending_onboarding",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      o.ID,
							ObjectLabel:    fmt.Sprintf("%s · 原单 %s", tenantName, o.ID),
							Impact:         "已确认支付，服务开通正在同步中，请回查原单切勿重复付款。",
							Status:         cont.LifecycleStage,
							Domain:         cont.NextStep.Domain,
							Owner:          "merchant",
							CanActorHandle: true,
							TargetPath:     "/work/billing",
							NextStep:       cont.NextStep.Summary,
						})
					case billing.StageHardwarePending:
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:hw:" + o.ID,
							Category:       "hardware_acceptance",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      o.ID,
							ObjectLabel:    fmt.Sprintf("%s · 硬件原单 %s", tenantName, o.ID),
							Impact:         "硬件已付款但实体 NFC 卡尚未完成出库/绑定/写卡/回读/现场验收。",
							Status:         cont.LifecycleStage,
							Domain:         billing.DomainHardwareDelivery,
							Owner:          cont.NextStep.Owner,
							CanActorHandle: canConfigStore,
							TargetPath:     "/work/touchpoints",
							NextStep:       cont.NextStep.Summary,
						})
					case billing.StageRefundInProgress, billing.StageFailed:
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:exc:" + o.ID,
							Category:       "exception",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      o.ID,
							ObjectLabel:    fmt.Sprintf("%s · 订单异常 %s", tenantName, o.ID),
							Impact:         cont.NextStep.Summary,
							Status:         cont.LifecycleStage,
							Domain:         cont.NextStep.Domain,
							Owner:          cont.NextStep.Owner,
							CanActorHandle: false,
							TargetPath:     "/work/billing",
							NextStep:       cont.NextStep.Summary,
						})
					}
				}
			}

			// 订阅尚未开通或已到期/临期(≤14天)
			switch effective {
			case billing.SubStatusUnconfirmed:
				if !hasActivePendingOrder {
					owner := "merchant"
					if canPay {
						owner = "agent"
					}
					nextStep := "当前代理无独立付款授权，请交由商家负责人查看生效目录并完成开通。"
					if canPay {
						nextStep = "进入商家账单页查看生效目录报价并按独立代付授权办理开通。"
					}
					merchantTodos = append(merchantTodos, agencyTodoItem{
						ID:             "todo:onboard:" + rel.TenantID,
						Category:       "pending_onboarding",
						TenantID:       rel.TenantID,
						TenantName:     tenantName,
						RelationID:     rel.ID,
						ObjectRef:      rel.TenantID,
						ObjectLabel:    tenantName + " · 服务开通",
						Impact:         "当前无有效订阅服务，新增收费能力受限。",
						Status:         effective,
						Domain:         billing.DomainPayment,
						Owner:          owner,
						CanActorHandle: canPay,
						TargetPath:     "/work/billing",
						NextStep:       nextStep,
					})
				}
			case billing.SubStatusExpired:
				owner := "merchant"
				if canPay {
					owner = "agent"
				}
				nextStep := "服务已到期，当前代理无独立付款授权，请交由商家负责人续费（旧权益仍可核销）。"
				if canPay {
					nextStep = "进入商家账单页查看续费报价并办理续费（旧权益不被追溯削减）。"
				}
				merchantTodos = append(merchantTodos, agencyTodoItem{
					ID:             "todo:renew:" + rel.TenantID,
					Category:       "renewal",
					TenantID:       rel.TenantID,
					TenantName:     tenantName,
					RelationID:     rel.ID,
					ObjectRef:      firstNonEmptyStr(fact.SubscriptionRef, rel.TenantID),
					ObjectLabel:    tenantName + " · 到期续费",
					Impact:         "订阅已到期，新增收费能力暂停；已发放权益仍可查询核销。",
					Status:         effective,
					Domain:         billing.DomainPayment,
					Owner:          owner,
					CanActorHandle: canPay,
					TargetPath:     "/work/billing",
					NextStep:       nextStep,
				})
			case billing.SubStatusActive:
				if fact.PaidUntil != "" {
					if exp, perr := time.Parse(time.RFC3339, fact.PaidUntil); perr == nil && exp.Sub(nowTime) <= 14*24*time.Hour {
						owner := "merchant"
						if canPay {
							owner = "agent"
						}
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:renew-soon:" + rel.TenantID,
							Category:       "renewal",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      firstNonEmptyStr(fact.SubscriptionRef, rel.TenantID),
							ObjectLabel:    tenantName + " · 临期续费",
							Impact:         fmt.Sprintf("服务期将于 %s 到期（不足 14 天），建议提前续费接续原服务期。", fact.PaidUntil[:10]),
							Status:         "expiring_soon",
							Domain:         billing.DomainPayment,
							Owner:          owner,
							CanActorHandle: canPay,
							TargetPath:     "/work/billing",
							NextStep:       "进入商家账单页查看续费报价，未到期续费自动从原到期日顺延。",
						})
					}
				}
			}

			// 实卡交付独立异常(例如换绑后 stale 或回读不一致,即便未挂到单条异常订单也明确暴露)。
			if derr == nil {
				for _, dv := range deliveries {
					if dv.Status == "superseded" {
						continue
					}
					if dv.BindingStale {
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:stale:" + dv.ID,
							Category:       "hardware_acceptance",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      dv.ID,
							ObjectLabel:    fmt.Sprintf("%s · 实体卡 %s", tenantName, dv.ID),
							Impact:         "门店触点绑定已变更，旧实测已失效，需重新核对验收。",
							Status:         "binding_stale",
							Domain:         billing.DomainHardwareDelivery,
							Owner:          "merchant",
							CanActorHandle: canConfigStore,
							TargetPath:     "/work/touchpoints",
							NextStep:       "进入门店投放入口重新完成回读与现场验收。",
						})
					} else if dv.LastReadbackReason != "" && !dv.LastReadbackMatches {
						merchantTodos = append(merchantTodos, agencyTodoItem{
							ID:             "todo:readback:" + dv.ID,
							Category:       "exception",
							TenantID:       rel.TenantID,
							TenantName:     tenantName,
							RelationID:     rel.ID,
							ObjectRef:      dv.ID,
							ObjectLabel:    fmt.Sprintf("%s · 实卡回读异常 %s", tenantName, dv.ID),
							Impact:         fmt.Sprintf("最近一次实卡回读不一致（%s），未通过验证。", dv.LastReadbackReason),
							Status:         "readback_mismatch",
							Domain:         billing.DomainHardwareDelivery,
							Owner:          "merchant",
							CanActorHandle: canConfigStore,
							TargetPath:     "/work/touchpoints",
							NextStep:       "进入门店投放入口重新写卡并回读核对。",
						})
					}
				}
			}
		}

		todos = append(todos, merchantTodos...)
		merchants = append(merchants, map[string]any{
			"relation_id":     rel.ID,
			"tenant_id":       rel.TenantID,
			"tenant_name":     tenantName,
			"allowed_actions": rel.AllowedActions,
			"store_scope_set": rel.StoreScopeSet,
			"valid_until":     rel.ValidUntil,
			"grant_revision":  rel.GrantRevision,
			"payer_granted":   rel.PayerGrantRef != "",
			"acting_context": map[string]any{
				"acting_on_behalf_of": tenantName,
				"allowed_actions":     rel.AllowedActions,
				"payer_granted":       rel.PayerGrantRef != "",
				"exit_clears_context": true,
			},
			"todo_count": len(merchantTodos),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"as_of":     nowStr,
		"merchants": merchants,
		"items":     todos,
	})
}
