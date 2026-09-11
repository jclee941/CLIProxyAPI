package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const cdpIdentityExpression = `(() => ({origin: location.origin, S06Grb: window.WIZ_global_data?.S06Grb, W3Yyqf: window.WIZ_global_data?.W3Yyqf, qDCSke: window.WIZ_global_data?.qDCSke}))()`
const geminiCDPOrigin = "https://gemini.google.com"

var cdpGaiaPattern = regexp.MustCompile(`^[0-9]{21}$`)

type cdpTarget struct {
	ID        string `json:"targetId"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	ContextID string `json:"browserContextId"`
}

type cdpAccountTab struct {
	target   cdpTarget
	session  string
	digest   string
	authUser uint64
}

func (source *cdpCredentialSource) Capture(parent context.Context, request captureRequest) (captured capturedSession, err error) {
	ctx, cancel := context.WithTimeout(parent, credentialWorkerBudget)
	defer cancel()
	defer func() {
		deadline, _ := ctx.Deadline()
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			captured = capturedSession{}
			err = failure(499, "source_cancelled")
		}
	}()
	wire, err := source.connect(ctx)
	if err != nil {
		return capturedSession{}, err
	}
	defer func() {
		if errClose := wire.close(); errClose != nil && err == nil {
			captured = capturedSession{}
			err = errClose
		}
	}()
	tabs, err := wire.accountTabs()
	if err != nil {
		return capturedSession{}, err
	}
	selected, err := selectCDPTab(tabs, request)
	if err != nil {
		return capturedSession{}, err
	}
	token, err := wire.captureCookies(selected)
	if err != nil {
		return capturedSession{}, err
	}
	for _, tab := range tabs {
		if tab.target.ContextID != selected.target.ContextID {
			continue
		}
		digest, errIdentity := wire.identity(tab.session)
		info, errInfo := wire.targetInfo(tab.session)
		index, eligible := geminiTargetAuthUser(info)
		if errIdentity != nil || errInfo != nil || digest != tab.digest || info.ID != tab.target.ID || info.ContextID != tab.target.ContextID || !eligible || index != tab.authUser {
			return capturedSession{}, failure(409, "source_identity_changed")
		}
	}
	return capturedSession{Token: token, AccountSHA256: request.Binding.ExpectedGaiaSHA256}, nil
}

func (wire *cdpWire) accountTabs() ([]cdpAccountTab, error) {
	result, err := cdpCall[struct {
		Targets []cdpTarget `json:"targetInfos"`
	}](wire, cdpCommand{Method: "Target.getTargets"})
	if err != nil {
		return nil, err
	}
	sort.Slice(result.Targets, func(first, second int) bool { return result.Targets[first].ID < result.Targets[second].ID })
	var tabs []cdpAccountTab
	for _, target := range result.Targets {
		index, eligible := geminiTargetAuthUser(target)
		if !eligible {
			continue
		}
		if target.ID == "" {
			return nil, failure(503, "source_unavailable")
		}
		attached, err := cdpCall[struct {
			Session string `json:"sessionId"`
		}](wire, cdpCommand{Method: "Target.attachToTarget", Params: cdpParams{TargetID: target.ID, Flatten: true}})
		if err != nil {
			return nil, err
		}
		if attached.Session == "" {
			return nil, failure(502, "source_protocol_failed")
		}
		wire.sessions = append(wire.sessions, attached.Session)
		info, err := wire.targetInfo(attached.Session)
		if err != nil {
			return nil, err
		}
		infoIndex, infoEligible := geminiTargetAuthUser(info)
		if info.ID != target.ID || info.ContextID == "" || (target.ContextID != "" && info.ContextID != target.ContextID) || !infoEligible || infoIndex != index {
			return nil, failure(503, "source_unavailable")
		}
		digest, err := wire.identity(attached.Session)
		if err != nil {
			return nil, err
		}
		tabs = append(tabs, cdpAccountTab{target: info, session: attached.Session, digest: digest, authUser: index})
	}
	return tabs, nil
}

func selectCDPTab(tabs []cdpAccountTab, request captureRequest) (cdpAccountTab, error) {
	var selected cdpAccountTab
	eligible := false
	for _, tab := range tabs {
		if tab.authUser != request.AuthUser {
			continue
		}
		eligible = true
		if tab.digest != request.Binding.ExpectedGaiaSHA256 {
			continue
		}
		if selected.session != "" && selected.target.ContextID != tab.target.ContextID {
			return cdpAccountTab{}, failure(409, "source_ambiguous")
		}
		if selected.session == "" {
			selected = tab
		}
	}
	if !eligible {
		return cdpAccountTab{}, failure(503, "source_unavailable")
	}
	if selected.session == "" {
		return cdpAccountTab{}, failure(412, "source_identity_unavailable")
	}
	for _, tab := range tabs {
		if tab.target.ContextID == selected.target.ContextID && tab.digest != selected.digest {
			return cdpAccountTab{}, failure(409, "source_ambiguous")
		}
	}
	return selected, nil
}

func geminiTargetAuthUser(target cdpTarget) (uint64, bool) {
	address, err := url.Parse(target.URL)
	if err != nil || target.Type != "page" || address.Scheme != "https" || address.Host != "gemini.google.com" || address.User != nil || address.RawPath != "" {
		return 0, false
	}
	if !strings.HasPrefix(address.Path, "/u/") {
		return 0, address.Path != "/u"
	}
	segment, _, _ := strings.Cut(strings.TrimPrefix(address.Path, "/u/"), "/")
	index, err := strconv.ParseUint(segment, 10, 64)
	return index, err == nil && strconv.FormatUint(index, 10) == segment
}

func (wire *cdpWire) targetInfo(session string) (cdpTarget, error) {
	result, err := cdpCall[struct {
		Target cdpTarget `json:"targetInfo"`
	}](wire, cdpCommand{Method: "Target.getTargetInfo", SessionID: session})
	return result.Target, err
}

func (wire *cdpWire) identity(session string) (string, error) {
	result, err := cdpCall[struct {
		Result struct {
			Type  string `json:"type"`
			Value struct {
				Origin    string `json:"origin"`
				Primary   string `json:"S06Grb"`
				Secondary string `json:"W3Yyqf"`
				Tertiary  string `json:"qDCSke"`
			} `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}](wire, cdpCommand{Method: "Runtime.evaluate", SessionID: session, Params: cdpParams{Expression: cdpIdentityExpression, ReturnByValue: true, Silent: true, ThrowOnSideEffect: true}})
	if err != nil {
		return "", err
	}
	identity := result.Result.Value
	if len(result.Exception) != 0 || result.Result.Type != "object" || identity.Origin != geminiCDPOrigin || !cdpGaiaPattern.MatchString(identity.Primary) || identity.Primary != identity.Secondary || identity.Primary != identity.Tertiary {
		return "", failure(412, "source_identity_unavailable")
	}
	digest := sha256.Sum256([]byte(identity.Primary))
	return hex.EncodeToString(digest[:]), nil
}

func (wire *cdpWire) captureCookies(tab cdpAccountTab) (sessionToken, error) {
	appURL := geminiCDPOrigin + "/app"
	if tab.authUser != 0 {
		appURL = geminiCDPOrigin + "/u/" + strconv.FormatUint(tab.authUser, 10) + "/app"
	}
	result, err := cdpCall[struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	}](wire, cdpCommand{Method: "Network.getCookies", SessionID: tab.session, Params: cdpParams{URLs: []string{appURL}}})
	if err != nil {
		return sessionToken{}, err
	}
	cookies := make([]string, 0, len(result.Cookies))
	for _, cookie := range result.Cookies {
		if err := (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).Valid(); err != nil {
			return sessionToken{}, failure(502, "source_protocol_failed")
		}
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	payload, err := json.Marshal(struct {
		Cookie   string `json:"cookie"`
		AuthUser uint64 `json:"auth_user"`
	}{strings.Join(cookies, "; "), tab.authUser})
	if err != nil {
		return sessionToken{}, failure(502, "source_protocol_failed")
	}
	token, err := parseToken("gemini-web:v1:" + base64.RawURLEncoding.EncodeToString(payload))
	if err != nil {
		return sessionToken{}, failure(502, "source_protocol_failed")
	}
	return token, nil
}
