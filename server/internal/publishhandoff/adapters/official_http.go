package adapters

// Official protocol clients. Their credentials configure transport only;
// channeladmission remains the authority for app/method/scenario eligibility.
import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func officialClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
func jsonCall(ctx context.Context, client *http.Client, method, target string, body io.Reader, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("invalid official request")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("official transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("official HTTP status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return fmt.Errorf("invalid official response")
	}
	return nil
}
func httpsEndpoint(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

// DouyinHTTP implements official client_token/openTicket/signature/share-id.
// Share results have no generic query endpoint in the cited H5 contract.
// Optional QueryURL is a deployment's trusted result service, never a browser
// URL or customer-supplied receipt. Absence leaves the official result unknown.
type DouyinHTTP struct {
	ClientKey, ClientSecret, BaseURL, QueryURL, QueryBearer string
	Client                                                  *http.Client
	mu                                                      sync.Mutex
	token, ticket                                           string
	tokenUntil, ticketUntil                                 time.Time
}

func NewDouyinHTTP(key, secret, queryURL, bearer string) *DouyinHTTP {
	return &DouyinHTTP{ClientKey: key, ClientSecret: secret, BaseURL: "https://open.douyin.com", QueryURL: queryURL, QueryBearer: bearer, Client: officialClient()}
}
func (c *DouyinHTTP) credential(ctx context.Context, ticket bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.token == "" || !now.Before(c.tokenUntil) {
		b, _ := json.Marshal(map[string]string{"client_key": c.ClientKey, "client_secret": c.ClientSecret, "grant_type": "client_credential"})
		var r struct {
			Data struct {
				AccessToken string `json:"access_token"`
				ExpiresIn   int64  `json:"expires_in"`
				ErrorCode   int    `json:"error_code"`
			} `json:"data"`
		}
		if err := jsonCall(ctx, c.Client, "POST", c.BaseURL+"/oauth/client_token/", bytes.NewReader(b), map[string]string{"Content-Type": "application/json"}, &r); err != nil {
			return "", err
		}
		if r.Data.ErrorCode != 0 || r.Data.AccessToken == "" || r.Data.ExpiresIn <= 60 {
			return "", fmt.Errorf("douyin client token refused")
		}
		c.token = r.Data.AccessToken
		c.tokenUntil = now.Add(time.Duration(r.Data.ExpiresIn-60) * time.Second)
	}
	if !ticket {
		return c.token, nil
	}
	if c.ticket == "" || !now.Before(c.ticketUntil) {
		var r struct {
			Data struct {
				Ticket    string `json:"ticket"`
				ExpiresIn int64  `json:"expires_in"`
				ErrorCode int    `json:"error_code"`
			} `json:"data"`
		}
		if err := jsonCall(ctx, c.Client, "GET", c.BaseURL+"/open/getticket/", nil, map[string]string{"access-token": c.token, "Content-Type": "application/json"}, &r); err != nil {
			return "", err
		}
		if r.Data.ErrorCode != 0 || r.Data.Ticket == "" || r.Data.ExpiresIn <= 60 {
			return "", fmt.Errorf("douyin open ticket refused")
		}
		c.ticket = r.Data.Ticket
		c.ticketUntil = now.Add(time.Duration(r.Data.ExpiresIn-60) * time.Second)
	}
	return c.ticket, nil
}
func (c *DouyinHTTP) SignShare(ctx context.Context, in DouyinShareSignInput) (map[string]string, error) {
	ticket, err := c.credential(ctx, true)
	if err != nil {
		return nil, err
	}
	nonce := newAdapterID("")
	sum := md5.Sum([]byte("nonce_str=" + nonce + "&ticket=" + ticket + "&timestamp=" + in.TimestampSec))
	return map[string]string{"nonce_str": nonce, "timestamp": in.TimestampSec, "signature": hex.EncodeToString(sum[:])}, nil
}
func (c *DouyinHTTP) NewShareID(ctx context.Context) (string, error) {
	tk, err := c.credential(ctx, false)
	if err != nil {
		return "", err
	}
	var r struct {
		Data struct {
			ShareID   string `json:"share_id"`
			ErrorCode int    `json:"error_code"`
		} `json:"data"`
	}
	if err := jsonCall(ctx, c.Client, "POST", c.BaseURL+"/share-id/?need_callback=true", strings.NewReader("{}"), map[string]string{"access-token": tk, "Content-Type": "application/json"}, &r); err != nil {
		return "", err
	}
	if r.Data.ErrorCode != 0 || r.Data.ShareID == "" {
		return "", fmt.Errorf("douyin share_id refused")
	}
	return r.Data.ShareID, nil
}
func (c *DouyinHTTP) QueryShare(ctx context.Context, id string) (string, bool, string, map[string]string, error) {
	if !httpsEndpoint(c.QueryURL) || c.QueryBearer == "" {
		return "unknown", false, "", map[string]string{"reason": "未配置受信官方结果查询服务"}, nil
	}
	var r struct {
		ClientKey   string `json:"client_key"`
		ShareID     string `json:"share_id"`
		State       string `json:"state"`
		Published   bool   `json:"published"`
		ItemID      string `json:"item_id"`
		EvidenceRef string `json:"evidence_ref"`
	}
	q := url.Values{"share_id": {id}, "client_key": {c.ClientKey}}
	if err := jsonCall(ctx, c.Client, "GET", c.QueryURL+"?"+q.Encode(), nil, map[string]string{"Authorization": "Bearer " + c.QueryBearer}, &r); err != nil {
		return "", false, "", nil, err
	}
	if r.ShareID != id || r.ClientKey != c.ClientKey || r.State == "" || r.Published && (r.State != "published" || r.ItemID == "" || r.EvidenceRef == "") {
		return "", false, "", nil, fmt.Errorf("douyin result binding/evidence missing")
	}
	return r.State, r.Published, r.ItemID, map[string]string{"evidence_ref": r.EvidenceRef}, nil
}

type XhsVerifyConfig struct {
	AppKey    string `json:"appKey"`
	Nonce     string `json:"nonce"`
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
}
type XhsSigner interface {
	Sign(context.Context) (XhsVerifyConfig, error)
}
type XhsHTTP struct {
	AppKey, AppSecret, TokenURL string
	Client                      *http.Client
	mu                          sync.Mutex
	token                       string
	expires                     time.Time
}

func NewXhsHTTP(key, secret string) *XhsHTTP {
	return &XhsHTTP{AppKey: key, AppSecret: secret, TokenURL: "https://edith.xiaohongshu.com/api/sns/v1/ext/access/token", Client: officialClient()}
}

// Signature follows the official JS guide's Java example: raw sorted keys,
// timeStamp case, secret suffix (first appSecret, then access_token).
func xhsSignature(key, nonce, stamp, secret string) string {
	sum := sha256.Sum256([]byte("appKey=" + key + "&nonce=" + nonce + "&timeStamp=" + stamp + secret))
	return hex.EncodeToString(sum[:])
}
func (c *XhsHTTP) Sign(ctx context.Context) (XhsVerifyConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	nonce := newAdapterID("")
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	if c.token == "" || !now.Add(time.Minute).Before(c.expires) {
		b, _ := json.Marshal(map[string]any{"app_key": c.AppKey, "nonce": nonce, "timestamp": now.UnixMilli(), "signature": xhsSignature(c.AppKey, nonce, stamp, c.AppSecret)})
		var r struct {
			Success     *bool  `json:"success"`
			Code        int    `json:"code"`
			AccessToken string `json:"access_token"`
			ExpiresIn   int64  `json:"expires_in"`
			Data        struct {
				AccessToken string `json:"access_token"`
				ExpiresIn   int64  `json:"expires_in"`
			} `json:"data"`
		}
		if err := jsonCall(ctx, c.Client, "POST", c.TokenURL, bytes.NewReader(b), map[string]string{"Content-Type": "application/json"}, &r); err != nil {
			return XhsVerifyConfig{}, err
		}
		tk, exp := r.AccessToken, r.ExpiresIn
		if tk == "" {
			tk, exp = r.Data.AccessToken, r.Data.ExpiresIn
		}
		if r.Code != 0 || (r.Success != nil && !*r.Success) || tk == "" || exp <= now.Add(time.Minute).UnixMilli() {
			return XhsVerifyConfig{}, fmt.Errorf("xiaohongshu token refused or expired")
		}
		c.token = tk
		c.expires = time.UnixMilli(exp)
		if c.expires.After(now.Add(24 * time.Hour)) {
			c.expires = now.Add(24 * time.Hour)
		}
	}
	return XhsVerifyConfig{AppKey: c.AppKey, Nonce: nonce, Timestamp: stamp, Signature: xhsSignature(c.AppKey, nonce, stamp, c.token)}, nil
}
