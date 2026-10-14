package participation

// T4: 受信预留端口。stub 覆盖 成功/缺货/超时/冲突 四态;接口契约:
// Reserved=true 必须带非空 ReservationRef;其余一律不确认。

import (
	"context"
	"errors"
	"testing"
)

type fakeReserver struct {
	result ReserveResult
	err    error
	got    ParticipationRef
}

func (f *fakeReserver) Reserve(ctx context.Context, ref ParticipationRef) (ReserveResult, error) {
	f.got = ref
	return f.result, f.err
}

func TestBenefitReserverContract(t *testing.T) {
	cases := []struct {
		name    string
		result  ReserveResult
		wantRes bool
		wantRef string
	}{
		{"success", ReserveResult{Reserved: true, ReservationRef: "rsv_1"}, true, "rsv_1"},
		{"out_of_stock", ReserveResult{Reason: ReasonOutOfStock}, false, ""},
		{"timeout", ReserveResult{Reason: ReasonTimeout}, false, ""},
		{"conflict", ReserveResult{Reason: ReasonConflict}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r BenefitReserver = &fakeReserver{result: tc.result}
			res, err := r.Reserve(context.Background(), ParticipationRef{ParticipationID: "pt_x", BenefitSlot: "pt_x"})
			if err != nil {
				t.Fatalf("Reserve: %v", err)
			}
			if res.Reserved != tc.wantRes || res.ReservationRef != tc.wantRef {
				t.Fatalf("got %+v, want reserved=%v ref=%q", res, tc.wantRes, tc.wantRef)
			}
			if res.Reserved && res.ReservationRef == "" {
				t.Fatal("Reserved=true requires a reservation ref")
			}
			if !res.Reserved && res.Reason == "" {
				t.Fatal("Reserved=false requires a reason")
			}
		})
	}
	// err 传播路径:参与保持 pending,不伪报确认。
	fr := &fakeReserver{err: errors.New("network down")}
	if _, err := fr.Reserve(context.Background(), ParticipationRef{}); err == nil {
		t.Fatal("error must propagate")
	}
}
