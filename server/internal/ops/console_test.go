package ops

import "testing"

func TestLookupActionRegistryFrozen(t *testing.T) {
	// 每个白名单动作都能查到且绑定正确命令。
	cases := map[string]string{
		ActionTenantSuspend: CmdTenantsManage, ActionTenantResume: CmdTenantsManage,
		ActionChargeHoldSet: CmdTenantsManage, ActionChargeHoldClear: CmdTenantsManage,
		ActionPlanPublish: CmdPlansManage, ActionPlanRetire: CmdPlansManage,
		ActionOrderRecheck:   CmdOrdersManage,
		ActionChannelDisable: CmdChannelsManage, ActionChannelRestore: CmdChannelsManage,
		ActionContentRetract: CmdTemplatesManage, ActionAssignmentCancel: CmdTemplatesManage,
		ActionTicketOpen: CmdOpsManage, ActionTicketConfirm: CmdOpsManage,
		ActionTicketResolve: CmdOpsManage, ActionTicketClose: CmdOpsManage,
		ActionDrillRecord: CmdOpsManage,
	}
	for action, want := range cases {
		spec, ok := LookupAction(action)
		if !ok {
			t.Fatalf("action %s not registered", action)
		}
		if spec.Command != want {
			t.Fatalf("action %s command = %s want %s", action, spec.Command, want)
		}
	}
	// 白名单外动作不可分派。
	if _, ok := LookupAction("wifi.turn_off"); ok {
		t.Fatal("non-whitelisted action must not dispatch (O4 不能关闭无关导航)")
	}
	// tenant 动作带 revision 守卫,其他资源不假装有。
	if spec, _ := LookupAction(ActionTenantSuspend); !spec.Revisioned {
		t.Fatal("tenant actions must be revisioned")
	}
	if spec, _ := LookupAction(ActionChannelDisable); spec.Revisioned {
		t.Fatal("channel rows have no revision column; must not claim a guard")
	}
}

func TestValidateEnvelope(t *testing.T) {
	if err := ValidateEnvelope("wifi.turn_off", "r", "k", ""); err == nil {
		t.Fatal("unknown action rejected")
	}
	if err := ValidateEnvelope(ActionTenantSuspend, "", "k", "1"); err != ErrReasonRequired {
		t.Fatalf("missing reason = %v", err)
	}
	if err := ValidateEnvelope(ActionTenantSuspend, "原因", "", ""); err != ErrIdempotencyKeyRequired {
		t.Fatalf("missing idem key = %v", err)
	}
	if err := ValidateEnvelope(ActionTenantSuspend, "原因", "k1", ""); err != ErrRevisionRequired {
		t.Fatalf("missing revision on revisioned resource = %v", err)
	}
	if err := ValidateEnvelope(ActionTenantSuspend, "原因", "k1", "2"); err != nil {
		t.Fatalf("valid envelope = %v", err)
	}
	// 非 revision 资源可以不带 revision。
	if err := ValidateEnvelope(ActionChannelDisable, "渠道失效", "k2", ""); err != nil {
		t.Fatalf("channel without revision = %v", err)
	}
}

func TestTicketStateMachine(t *testing.T) {
	if s, err := TicketTransition(TicketOpen, TicketConfirmed, "", ""); err != nil || s != TicketConfirmed {
		t.Fatalf("open→confirmed = %v %v", s, err)
	}
	if _, err := TicketTransition(TicketConfirmed, TicketResolved, "", ""); err == nil {
		t.Fatal("resolve without resolution must fail")
	}
	if _, err := TicketTransition(TicketConfirmed, TicketResolved, "已修复探针", ""); err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if _, err := TicketTransition(TicketResolved, TicketClosed, "", ""); err != ErrTicketCloseReason {
		t.Fatalf("close without reason = %v", err)
	}
	if _, err := TicketTransition(TicketResolved, TicketClosed, "", "恢复确认,证据见演练 dr_1"); err != nil {
		t.Fatalf("close = %v", err)
	}
	// 终态与跳跃迁移全部拒绝。
	for _, c := range [][2]string{
		{TicketClosed, TicketOpen}, {TicketClosed, TicketConfirmed},
		{TicketOpen, TicketResolved}, {TicketConfirmed, TicketClosed},
		{TicketResolved, TicketOpen},
	} {
		if _, err := TicketTransition(c[0], c[1], "x", "y"); err == nil {
			t.Fatalf("transition %v→%v must fail", c[0], c[1])
		}
	}
}

func TestValidateDrill(t *testing.T) {
	if err := ValidateDrill(DrillRestore, "ok", ""); err == nil {
		t.Fatal("restore drill without verification report must fail")
	}
	if err := ValidateDrill(DrillRestore, "failed", "回读校验:3/5 表校验和不一致"); err != nil {
		t.Fatalf("failed restore drill with report is honest evidence: %v", err)
	}
	if err := ValidateDrill(DrillBackup, "ok", ""); err != nil {
		t.Fatalf("backup drill = %v", err)
	}
	if err := ValidateDrill("reboot", "ok", ""); err == nil {
		t.Fatal("unknown drill kind rejected")
	}
}
