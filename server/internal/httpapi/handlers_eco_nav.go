package httpapi

// HUI-2628: the server-side half of the app-switch BFF. The browser sends only
// {app_id, launch_target_id}; public-ai's shell launch is the only authority
// that mints the one-time code and the target URL. Nothing about the current
// campaign, assets, payer or project is accepted (DisallowUnknownFields).
//
// HUI-2222 (wave2, M11): the hand-off to the browser is two-phase with 3546
// credential discipline. Mint answers a short-lived single-use jump token
// (jtg_, SHA-256 hashed at rest, bound to principal+tenant) — never the
// launch URL. Redeem claims the grant transactionally and only then releases
// the shell's URL, so a leaked mint response cannot carry a usable target
// and a leaked URL cannot be replayed.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/appjump"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
)

var ecoNavTargetID = regexp.MustCompile(`^ti-[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

type ecoNavSwitchBody struct {
	AppID          string `json:"app_id"`
	LaunchTargetID string `json:"launch_target_id"`
}

func validEcoNavAppID(raw string) bool {
	if len(raw) < 3 || len(raw) > 64 {
		return false
	}
	for _, r := range raw {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return raw[0] >= 'a' && raw[0] <= 'z'
}

func safeLaunchURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}

func (s *Server) handleEcoNavSwitch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "membership required")
		return
	}
	if !c.Member.Enabled {
		fail(w, http.StatusForbidden, "member_disabled", "member disabled")
		return
	}
	var in ecoNavSwitchBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "app_id and launch_target_id are required")
		return
	}
	appID := strings.TrimSpace(in.AppID)
	targetID := strings.TrimSpace(in.LaunchTargetID)
	if !validEcoNavAppID(appID) || !ecoNavTargetID.MatchString(targetID) {
		fail(w, http.StatusBadRequest, "bad_request", "app_id or launch_target_id is invalid")
		return
	}
	if appID == s.Cfg.AppID {
		fail(w, http.StatusConflict, "current_app", "the current app does not need a switch")
		return
	}
	if s.ID == nil || strings.TrimSpace(s.Cfg.IdentityBaseURL) == "" || strings.TrimSpace(s.Cfg.IdentityToken) == "" {
		fail(w, http.StatusServiceUnavailable, "identity_unavailable", "platform identity is not configured; no launch was minted")
		return
	}
	launchURL, err := s.ID.LaunchShell(r.Context(), c.Principal.ID, c.Member.TenantID, appID, targetID)
	if err != nil {
		if errors.Is(err, identity.ErrShellInvalidResponse) {
			fail(w, http.StatusBadGateway, "launch_url_rejected", "the shell returned no usable https launch_url")
			return
		}
		var he *identity.HTTPError
		if errors.As(err, &he) {
			switch he.Status {
			case http.StatusBadRequest:
				fail(w, http.StatusBadRequest, "shell_invalid_request", "the shell rejected the switch request")
				return
			case http.StatusForbidden:
				fail(w, http.StatusForbidden, "shell_launch_denied", "the shell refused this launch")
				return
			case http.StatusTooManyRequests:
				fail(w, http.StatusTooManyRequests, "shell_rate_limited", "too many launches; try again later")
				return
			}
		}
		fail(w, http.StatusServiceUnavailable, "identity_unavailable", "platform identity could not mint a launch")
		return
	}
	if !safeLaunchURL(launchURL) {
		fail(w, http.StatusBadGateway, "launch_url_rejected", "the shell returned an unsafe launch_url")
		return
	}
	// HUI-2222: hand the URL to the browser only through a short-lived
	// single-use grant. The mint response never carries the launch_url.
	if s.Jumps == nil {
		fail(w, http.StatusServiceUnavailable, "jump_unavailable", "jump grants are not wired; no launch was handed out")
		return
	}
	minted, err := s.Jumps.Mint(c.Member.TenantID, c.Principal.ID, appID, targetID, launchURL)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "the jump credential could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jump_token": minted.Token,
		"grant_id":   minted.Grant.ID,
		"expires_at": minted.Grant.ExpiresAt,
	})
}

// handleEcoNavRedeem claims a jump grant and releases the shell's launch URL
// exactly once. Expired answers 410 (honest expiry, same as 3546); used,
// revoked, foreign-bound and malformed tokens all answer the uniform 404 so
// a probe cannot tell which check failed.
func (s *Server) handleEcoNavRedeem(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "membership required")
		return
	}
	if !c.Member.Enabled {
		fail(w, http.StatusForbidden, "member_disabled", "member disabled")
		return
	}
	var in struct {
		JumpToken string `json:"jump_token"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "jump_token is required")
		return
	}
	if s.Jumps == nil {
		fail(w, http.StatusServiceUnavailable, "jump_unavailable", "jump grants are not wired; nothing to redeem")
		return
	}
	grant, err := s.Jumps.Redeem(in.JumpToken, c.Principal.ID, c.Member.TenantID)
	if err != nil {
		switch {
		case errors.Is(err, appjump.ErrExpired):
			fail(w, http.StatusGone, "jump_expired", "this jump credential has expired; request a new switch")
		case errors.Is(err, appjump.ErrUsed), errors.Is(err, appjump.ErrRevoked),
			errors.Is(err, appjump.ErrNotFound), errors.Is(err, appjump.ErrTokenShape):
			fail(w, http.StatusNotFound, "not_found", "no redeemable jump credential")
		default:
			fail(w, http.StatusInternalServerError, "internal", "the jump credential could not be redeemed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"launch_url":       grant.LaunchURL,
		"app_id":           grant.AppID,
		"launch_target_id": grant.LaunchTargetID,
	})
}
