package adapters

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// KuaishouHTTP uses the website OAuth and content-management HTTP protocols.
// No private API, simulated user action, native-editor promise or retry of
// publish is hidden in this transport.
type KuaishouHTTP struct {
	AppID, AppSecret, BaseURL string
	Client                    *http.Client
}

func NewKuaishouHTTP(id, secret string) *KuaishouHTTP {
	return &KuaishouHTTP{AppID: id, AppSecret: secret, BaseURL: "https://open.kuaishou.com", Client: officialClient()}
}
func (c *KuaishouHTTP) query(t KsToken) url.Values {
	return url.Values{"app_id": {c.AppID}, "access_token": {t.AccessToken}}
}
func (c *KuaishouHTTP) ExchangeToken(ctx context.Context, code, redirect string) (KsToken, error) {
	q := url.Values{"app_id": {c.AppID}, "app_secret": {c.AppSecret}, "code": {code}, "grant_type": {"authorization_code"}}
	var r struct {
		Result       int      `json:"result"`
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		OpenID       string   `json:"open_id"`
		ExpiresIn    int64    `json:"expires_in"`
		Scopes       []string `json:"scopes"`
	}
	if err := jsonCall(ctx, c.Client, "GET", c.BaseURL+"/oauth2/access_token?"+q.Encode(), nil, nil, &r); err != nil {
		return KsToken{}, err
	}
	if r.Result != 1 || r.AccessToken == "" || r.OpenID == "" || r.ExpiresIn <= 0 {
		return KsToken{}, fmt.Errorf("kuaishou token exchange refused")
	}
	return KsToken{OpenID: r.OpenID, AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(r.ExpiresIn) * time.Second), Scope: strings.Join(r.Scopes, ",")}, nil
}
func (c *KuaishouHTTP) UploadMedia(ctx context.Context, t KsToken, kind, ctype string, b []byte) (string, error) {
	if len(b) == 0 {
		return "", fmt.Errorf("kuaishou empty media")
	}
	// Official publish takes the cover as a multipart file in the *publish*
	// request, rather than a separate cover-upload endpoint.
	if kind == "cover" {
		if len(b) > 10<<20 || !strings.HasPrefix(ctype, "image/") {
			return "", fmt.Errorf("kuaishou cover format/size invalid")
		}
		return base64.StdEncoding.EncodeToString(b), nil
	}
	q := c.query(t)
	var r struct {
		Result      int    `json:"result"`
		UploadToken string `json:"upload_token"`
		Endpoint    string `json:"endpoint"`
	}
	if err := jsonCall(ctx, c.Client, "POST", c.BaseURL+"/openapi/photo/start_upload?"+q.Encode(), nil, nil, &r); err != nil {
		return "", err
	}
	if r.Result != 1 || r.UploadToken == "" {
		return "", fmt.Errorf("kuaishou upload start refused")
	}
	// Only the official API's uploader hostname is accepted. Redirects are
	// disabled; a compromised endpoint cannot fetch arbitrary internal URLs.
	endpoint := strings.ToLower(r.Endpoint)
	u, err := url.Parse("https://" + endpoint)
	if err != nil || u.Host != endpoint || u.Port() != "" || u.Path != "" || (!strings.HasSuffix(u.Hostname(), ".gifshow.com") && !strings.HasSuffix(u.Hostname(), ".kuaishou.com")) {
		return "", fmt.Errorf("kuaishou upload endpoint refused")
	}
	// Prefer encrypted uploader transport. No automatic downgrade to HTTP.
	target := u.String()
	uq := url.Values{"upload_token": {r.UploadToken}}
	call := func(path string, query url.Values, raw []byte) error {
		var out struct {
			Result int `json:"result"`
		}
		if err := jsonCall(ctx, c.Client, "POST", target+path+"?"+query.Encode(), bytes.NewReader(raw), map[string]string{"Content-Type": ctype}, &out); err != nil {
			return err
		}
		if out.Result != 1 {
			return fmt.Errorf("kuaishou upload refused")
		}
		return nil
	}
	const chunk = 8 << 20
	if len(b) < 10<<20 {
		if err := call("/api/upload", uq, b); err != nil {
			return "", err
		}
	} else {
		count := (len(b) + chunk - 1) / chunk
		for i := 0; i < count; i++ {
			end := (i + 1) * chunk
			if end > len(b) {
				end = len(b)
			}
			uq.Set("fragment_id", strconv.Itoa(i))
			if err := call("/api/upload/fragment", uq, b[i*chunk:end]); err != nil {
				return "", err
			}
		}
		uq.Del("fragment_id")
		uq.Set("fragment_count", strconv.Itoa(count))
		if err := call("/api/upload/complete", uq, nil); err != nil {
			return "", err
		}
	}
	return r.UploadToken, nil
}
func (c *KuaishouHTTP) CreatePhoto(ctx context.Context, t KsToken, video, cover, caption string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(cover)
	if err != nil || len(raw) == 0 {
		return "", fmt.Errorf("kuaishou required cover missing")
	}
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	part, err := mp.CreateFormFile("cover", "cover.jpg")
	if err != nil {
		return "", err
	}
	_, _ = part.Write(raw)
	_ = mp.WriteField("caption", caption)
	_ = mp.Close()
	q := c.query(t)
	q.Set("upload_token", video)
	var r struct {
		Result    int `json:"result"`
		VideoInfo struct {
			PhotoID string `json:"photo_id"`
		} `json:"video_info"`
	}
	if err := jsonCall(ctx, c.Client, http.MethodPost, c.BaseURL+"/openapi/photo/publish?"+q.Encode(), &body, map[string]string{"Content-Type": mp.FormDataContentType()}, &r); err != nil {
		return "", err
	}
	if r.Result != 1 || r.VideoInfo.PhotoID == "" {
		return "", fmt.Errorf("kuaishou publish refused or result unknown")
	}
	return r.VideoInfo.PhotoID, nil
}
func (c *KuaishouHTTP) QueryPhoto(ctx context.Context, t KsToken, id string) (string, bool, map[string]string, error) {
	q := c.query(t)
	q.Set("photo_id", id)
	var r struct {
		Result    int `json:"result"`
		VideoInfo struct {
			PhotoID string `json:"photo_id"`
			Pending *bool  `json:"pending"`
			PlayURL string `json:"play_url"`
		} `json:"video_info"`
	}
	if err := jsonCall(ctx, c.Client, "GET", c.BaseURL+"/openapi/photo/info?"+q.Encode(), nil, nil, &r); err != nil {
		return "unknown", false, nil, err
	}
	if r.Result != 1 {
		return "unknown", false, map[string]string{"result": strconv.Itoa(r.Result)}, nil
	}
	if r.VideoInfo.PhotoID != id || r.VideoInfo.Pending == nil {
		return "unknown", false, nil, fmt.Errorf("kuaishou result binding/state incomplete")
	}
	detail := map[string]string{"photo_id": id, "visibility": "not_attested"}
	if *r.VideoInfo.Pending {
		return "pending", false, detail, nil
	}
	if r.VideoInfo.PlayURL == "" {
		return "unknown", false, detail, nil
	}
	return "published", true, detail, nil
}
