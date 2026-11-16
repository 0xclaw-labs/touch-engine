package adapters

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDouyinHTTPUsesOfficialIDSignatureAndCachesCredentials(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/oauth/client_token/":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["grant_type"] != "client_credential" || in["client_secret"] != "secret" {
				t.Errorf("wrong token request: %v", in)
			}
			fmt.Fprint(w, `{"data":{"access_token":"server-token","expires_in":7200,"error_code":0}}`)
		case "/open/getticket/":
			if r.Header.Get("access-token") != "server-token" {
				t.Error("missing token header")
			}
			fmt.Fprint(w, `{"data":{"ticket":"official-ticket","expires_in":7200,"error_code":0}}`)
		case "/share-id/":
			if r.Method != "POST" || r.URL.Query().Get("need_callback") != "true" {
				t.Error("incorrect share-id request")
			}
			fmt.Fprint(w, `{"data":{"share_id":"official-id","error_code":0}}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewDouyinHTTP("key", "secret", "", "")
	c.BaseURL = srv.URL
	c.Client = srv.Client()
	for i := 0; i < 2; i++ {
		id, err := c.NewShareID(context.Background())
		if err != nil || id != "official-id" {
			t.Fatalf("id=%q err=%v", id, err)
		}
		out, err := c.SignShare(context.Background(), DouyinShareSignInput{TimestampSec: "1650941858"})
		if err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum([]byte("nonce_str=" + out["nonce_str"] + "&ticket=official-ticket&timestamp=1650941858"))
		if out["signature"] != hex.EncodeToString(sum[:]) {
			t.Fatal("signature does not follow official MD5 recipe")
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "official-ticket") || strings.Contains(string(raw), "secret") {
			t.Fatal("credential leaked in browser params")
		}
	}
	if calls["/oauth/client_token/"] != 1 || calls["/open/getticket/"] != 1 {
		t.Fatalf("credentials refetched unnecessarily: %v", calls)
	}
}

func TestXhsHTTPSignatureAndTokenCache(t *testing.T) {
	if got := xhsSignature("123", "nonce", "1692102691696", "test"); got != "16c900bfce9d55a78406a8d5a0be0b04cd4881bde8ade0b7bc9ae7ea2d32c506" {
		t.Fatalf("official Java-example signature vector mismatch: %s", got)
	}
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in struct {
			AppKey    string `json:"app_key"`
			Nonce     string `json:"nonce"`
			Timestamp int64  `json:"timestamp"`
			Signature string `json:"signature"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.AppKey != "123" || in.Signature != xhsSignature("123", in.Nonce, fmt.Sprint(in.Timestamp), "test") {
			t.Error("token signature mismatched official keys/suffix")
		}
		fmt.Fprintf(w, `{"success":true,"data":{"access_token":"cached-official-token","expires_in":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	}))
	defer srv.Close()
	c := NewXhsHTTP("123", "test")
	c.TokenURL = srv.URL
	c.Client = srv.Client()
	first, err := c.Sign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Sign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || first.Nonce == second.Nonce || first.Signature != xhsSignature("123", first.Nonce, first.Timestamp, "cached-official-token") {
		t.Fatalf("cache/sign mismatch calls=%d", calls)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "cached-official-token") || strings.Contains(string(raw), "test") {
		t.Fatal("secret escaped public verifyConfig")
	}
}

func TestKuaishouHTTPPublishAndQueryAreDifferentFacts(t *testing.T) {
	pending := true
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("app_id") != "appid" {
			t.Error("official app_id missing")
		}
		switch r.URL.Path {
		case "/oauth2/access_token":
			if q.Get("app_secret") != "secret" || q.Get("grant_type") != "authorization_code" || q.Get("code") != "code" {
				t.Error("oauth protocol mismatch")
			}
			fmt.Fprint(w, `{"result":1,"access_token":"token","open_id":"account","expires_in":3600,"scopes":["user_video_publish","user_video_info"]}`)
		case "/openapi/photo/publish":
			if q.Get("access_token") != "token" || q.Get("upload_token") != "upload" {
				t.Error("publish grant missing")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			cover, _, err := r.FormFile("cover")
			if err != nil {
				t.Error("required cover absent")
			} else {
				b, _ := io.ReadAll(cover)
				if string(b) != "actual-cover" {
					t.Error("wrong cover bytes")
				}
			}
			if r.FormValue("caption") != "confirmed caption" {
				t.Error("caption absent")
			}
			fmt.Fprint(w, `{"result":1,"video_info":{"photo_id":"work","pending":false}}`)
		case "/openapi/photo/info":
			fmt.Fprintf(w, `{"result":1,"video_info":{"photo_id":"work","pending":%t,"play_url":"https://play.example/work"}}`, pending)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewKuaishouHTTP("appid", "secret")
	c.BaseURL = srv.URL
	c.Client = srv.Client()
	ctx := context.Background()
	tk, err := c.ExchangeToken(ctx, "code", "unused")
	if err != nil {
		t.Fatal(err)
	}
	cover, err := c.UploadMedia(ctx, tk, "cover", "image/jpeg", []byte("actual-cover"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.CreatePhoto(ctx, tk, "upload", cover, "confirmed caption")
	if err != nil || id != "work" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	state, published, _, err := c.QueryPhoto(ctx, tk, id)
	if err != nil || state != "pending" || published {
		t.Fatalf("pending is not publication: %q %t %v", state, published, err)
	}
	pending = false
	_, published, detail, err := c.QueryPhoto(ctx, tk, id)
	if err != nil || !published || detail["visibility"] != "not_attested" {
		t.Fatal("explicit final observation missing")
	}
}

type adapterRoundTrip func(*http.Request) (*http.Response, error)

func (f adapterRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestKuaishouHTTPUploadsActualVideoAndCompletesFragments(t *testing.T) {
	for _, size := range []int{17, 11 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			gotBytes, fragments, completes := 0, 0, 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/openapi/photo/start_upload":
					if r.Method != "POST" || r.URL.Query().Get("access_token") != "token" {
						t.Error("start upload grant/method mismatch")
					}
					fmt.Fprint(w, `{"result":1,"upload_token":"one-upload","endpoint":"uploader.fixture.gifshow.com"}`)
				case "/api/upload", "/api/upload/fragment":
					if r.URL.Query().Get("upload_token") != "one-upload" {
						t.Error("upload binding missing")
					}
					if r.Header.Get("Content-Type") != "video/mp4" {
						t.Error("media content type missing")
					}
					b, _ := io.ReadAll(r.Body)
					gotBytes += len(b)
					if r.URL.Path == "/api/upload/fragment" {
						if r.URL.Query().Get("fragment_id") != fmt.Sprint(fragments) {
							t.Error("fragment order mismatch")
						}
						fragments++
					}
					fmt.Fprint(w, `{"result":1}`)
				case "/api/upload/complete":
					completes++
					if r.URL.Query().Get("fragment_count") != "2" {
						t.Error("fragment count mismatch")
					}
					fmt.Fprint(w, `{"result":1}`)
				default:
					t.Errorf("unexpected upload path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			c := NewKuaishouHTTP("id", "secret")
			c.BaseURL = srv.URL
			c.Client = srv.Client()
			transport := c.Client.Transport
			c.Client.Transport = adapterRoundTrip(func(r *http.Request) (*http.Response, error) {
				clone := r.Clone(r.Context())
				u := *r.URL
				clone.URL = &u
				if r.URL.Host == "uploader.fixture.gifshow.com" {
					base, _ := url.Parse(srv.URL)
					clone.URL.Host = base.Host
				}
				return transport.RoundTrip(clone)
			})
			ref, err := c.UploadMedia(context.Background(), KsToken{AccessToken: "token"}, "video", "video/mp4", bytes.Repeat([]byte{7}, size))
			if err != nil || ref != "one-upload" || gotBytes != size {
				t.Fatalf("upload bytes/ref mismatch %d/%d %q %v", gotBytes, size, ref, err)
			}
			if size > 10<<20 && (fragments != 2 || completes != 1) {
				t.Fatalf("fragmented upload not completed: %d/%d", fragments, completes)
			}
		})
	}
}
