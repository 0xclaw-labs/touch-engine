package production

import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
)

func TestRequestQuoteUsesRealSourceProtocolAndNeverHoldsBeforeConfirmation(t *testing.T) {
 var paths []string
 srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  paths = append(paths, r.URL.Path)
  var body map[string]any
  _ = json.NewDecoder(r.Body).Decode(&body)
  if body["funding_version"]!=SourceReservationV1 || body["payer_account_id"]!="payer-a" || body["payer_user_id"]!="user-a" {t.Errorf("scope not carried: %+v",body)}
  if r.URL.Path == "/internal/v1/billing/unified/usage" { _,_ = w.Write([]byte(`{"fact":{"usage_id":"usage-a"}}`)); return }
  _,_ = w.Write([]byte(`{"quote":{"quote_id":"q-a","usage_id":"usage-a","amount_minor":8900,"expires_at_unix":4070908800,"pricing_version":"price-v1"}}`))
 }))
 defer srv.Close()
 q,err:=NewQuoteAuthority(srv.URL,"token","touch-engine").RequestQuote(context.Background(),QuoteRequest{PayerAccountID:"payer-a",PayerUserID:"user-a",PricingVersion:"price-v1",Deliverables:"视频一条",IdempotencyKey:"key-a",Brief:"hash-a"})
 if err!=nil||q.Ref!="q-a"||q.AmountMinor!=8900||q.ExpiresAt!="2099-01-01T00:00:00Z"{t.Fatalf("quote: %+v %v",q,err)}
 if len(paths)!=2||paths[0]!="/internal/v1/billing/unified/usage"||paths[1]!="/internal/v1/billing/unified/quote"{t.Fatalf("unexpected authority writes: %+v",paths)}
}
func TestConfirmOfferPinsExpectedAmountAndRecoversSameHold(t *testing.T){
 holdCalls:=0;held:=false
 expiry:=time.Now().Add(time.Hour).Unix()
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method==http.MethodGet {
   h:=any(nil);if held{h=map[string]any{"hold_id":"hold-one","status":"open","amount_minor":8900}}
   _=json.NewEncoder(w).Encode(map[string]any{"facts":map[string]any{"quote":map[string]any{"quote_id":"q1","amount_minor":8900,"expires_at_unix":expiry},"hold":h}});return
  }
  if r.URL.Path!="/internal/v1/billing/unified/hold"{t.Errorf("wrong confirmation endpoint: %s",r.URL.Path)}
  holdCalls++;var body map[string]any;_=json.NewDecoder(r.Body).Decode(&body)
  if body["expected_amount_minor"]!=float64(8900)||body["quote_id"]!="q1"||body["idempotency_key"]!="same:hold"{t.Errorf("unpinned hold: %+v",body)}
  held=true;_,_=w.Write([]byte(`{"hold":{"hold_id":"hold-one","usage_id":"usage-one","status":"open","amount_minor":8900}}`))
 }));defer srv.Close()
 client:=NewQuoteAuthority(srv.URL,"token","touch-engine")
 q:=Quote{Ref:"q1",UsageID:"usage-one",AmountMinor:8900,Currency:"CNY",ExpiresAt:time.Unix(expiry,0).UTC().Format(time.RFC3339),Deliverables:"视频一条"}
 in:=QuoteRequest{PayerAccountID:"payer-a",PayerUserID:"user-a",IdempotencyKey:"same"}
 first,err:=client.ConfirmOffer(context.Background(),in,q);if err!=nil{t.Fatal(err)}
 replay,err:=client.ConfirmOffer(context.Background(),in,q);if err!=nil||replay.ID!=first.ID||holdCalls!=1{t.Fatalf("duplicate hold: %+v %v calls=%d",replay,err,holdCalls)}
 q.AmountMinor=9000
 if _,err:=client.ConfirmOffer(context.Background(),in,q);!errors.Is(err,ErrOfferChanged){t.Fatalf("changed amount: %v",err)}
}
func TestExpiredOfferCannotOpenHold(t *testing.T){
 expiry:=time.Now().Add(-time.Hour).Unix();holdCalls:=0
 srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!="GET"{holdCalls++;t.Error("expired quote tried to open reservation")}
  _=json.NewEncoder(w).Encode(map[string]any{"facts":map[string]any{"quote":map[string]any{"quote_id":"q1","amount_minor":1,"expires_at_unix":expiry},"hold":nil}})
 }));defer srv.Close()
 _,err:=NewQuoteAuthority(srv.URL,"t","touch-engine").ConfirmOffer(context.Background(),QuoteRequest{},Quote{Ref:"q1",UsageID:"u",AmountMinor:1,Currency:"CNY",ExpiresAt:time.Unix(expiry,0).UTC().Format(time.RFC3339),Deliverables:"视频"})
 if !errors.Is(err,ErrOfferExpired)||holdCalls!=0{t.Fatalf("expired: %v calls=%d",err,holdCalls)}
}
