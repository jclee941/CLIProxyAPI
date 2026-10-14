package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The account identity is derived from the page, so tests must serve a page that yields the digest they assert on.
const testGaia = "123456789012345678901"

// testOtherGaia stands for a different Google account, which several tests need
// in order to express an identity that must be rejected.
const testOtherGaia = "987654321098765432109"

var (
	testAccountDigest      = gaiaDigest(testGaia)
	testOtherAccountDigest = gaiaDigest(testOtherGaia)
)

func gaiaDigest(gaia string) string {
	sum := sha256.Sum256([]byte(gaia))
	return hex.EncodeToString(sum[:])
}

func encodedTokenUser(cookie string, authUser int) string {
	return encodeWebCredential(webCredential{Cookie: cookie, AuthUser: authUser}).value
}

// nativeRPCID names the batchexecute RPC a request carries, or "" for any other
// request.
func nativeRPCID(request *http.Request) string {
	if !strings.HasSuffix(request.URL.Path, "/batchexecute") {
		return ""
	}
	return request.URL.Query().Get("rpcids")
}

// credentialRoute names the native credential and account routes so a switch
// over them reads as intent: the account page, the rotation endpoint and the
// capability and usage RPCs and the generation stream.
func credentialRoute(request *http.Request) string {
	switch {
	case strings.HasSuffix(request.URL.Path, "/app"):
		return "credential:inspect"
	case request.URL.Path == "/RotateCookies":
		return "credential:renew"
	case strings.HasSuffix(request.URL.Path, "/StreamGenerate"):
		return "account:generate"
	case nativeRPCID(request) == accountCapabilityRPC:
		return "account:models"
	case nativeRPCID(request) == webUsageRPC:
		return "account:usage"
	}
	return request.URL.Path
}

// capabilityPayload is the account capability reply for accounts offering the
// named models, each in its standard mode.
func capabilityPayload(names ...string) any {
	entries := make([]any, 0, len(names))
	for index, name := range names {
		id := "cap-" + strconv.Itoa(index)
		if name == "3.8 Flash" {
			id = "cap-flash"
		}
		entries = append(entries, slots(18, map[int]any{0: id, 11: name, 17: float64(1)}))
	}
	return slots(16, map[int]any{14: float64(1000), 15: entries})
}

// writeCapabilities answers the capability RPC for an account offering names.
func writeCapabilities(t *testing.T, writer http.ResponseWriter, names ...string) {
	t.Helper()
	writeFixture(t, writer, rpcEnvelope(t, accountCapabilityRPC, capabilityPayload(names...)))
}

// serveAccountRPC answers the account reads every listing, discovery and quota
// path makes: a Pro account with a Flash capability, 5h and weekly windows and no video
// cap. It reports whether the request was one of them.
func serveAccountRPC(t *testing.T, writer http.ResponseWriter, request *http.Request) bool {
	t.Helper()
	switch nativeRPCID(request) {
	case accountCapabilityRPC:
		writeCapabilities(t, writer, "3.8 Flash")
	case webUsageRPC:
		fiveHour := slots(4, map[int]any{0: float64(12), 1: 0.25, 2: float64(1), 3: []any{[]any{float64(1789363992)}}})
		weekly := slots(4, map[int]any{0: float64(48384), 1: float64(0), 2: float64(2), 3: []any{[]any{float64(1789950792)}}})
		writeFixture(t, writer, rpcEnvelope(t, webUsageRPC, []any{float64(2), []any{fiveHour, weekly}, false}))
	case webGxuBudgetRPC:
		writeFixture(t, writer, rpcEnvelope(t, webGxuBudgetRPC, []any{false}))
	default:
		return false
	}
	return true
}

const rotatingCookie = "__Secure-1PSIDTS"

// Every rotation must yield a value the jar does not already hold: handing back
// the current one leaves the credential unchanged and swallows the write, save
// and revision a renewal owes. Deriving it from the arriving jar keeps the
// result predictable, so a test can still name the token it expects.
func writeRotationFixture(writer http.ResponseWriter, request *http.Request) {
	http.SetCookie(writer, &http.Cookie{Name: rotatingCookie, Value: rotationValue(request), Path: "/", Domain: ".google.com", Secure: true})
	writer.WriteHeader(http.StatusOK)
}

func rotationValue(request *http.Request) string {
	cookie, err := request.Cookie(rotatingCookie)
	if err != nil || cookie.Value == "" {
		return "rotated"
	}
	return cookie.Value + "+"
}

func writeIdentityFixture(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := writer.Write([]byte(nativeIdentityPage(testGaia))); err != nil {
		t.Error(err)
	}
}

func nativeIdentityPage(gaia string) string {
	return `<!doctype html><script>window.WIZ_global_data = {"SNlM0e":"test-xsrf","cfb2h":"test-build","FdrFJe":"test-session",` +
		`"S06Grb":"` + gaia + `","W3Yyqf":"` + gaia + `","qDCSke":"` + gaia + `"};</script>`
}

// rotatedToken is what a renewal produces for a starting cookie, so a test can
// assert on the replacement without knowing how the jar is merged.
func rotatedToken(cookie string) string {
	return encodeWebCredential(webCredential{Cookie: webMergeCookies(cookie, map[string]string{rotatingCookie: "rotated"}), AuthUser: 2}).value
}

func nativeCredentials(t *testing.T, service *service) *httptest.Server {
	t.Helper()
	return nativeCredentialsAs(t, service, testGaia)
}

func nativeCredentialsAs(t *testing.T, service *service, gaia string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/RotateCookies":
			writeRotationFixture(writer, request)
		case strings.HasSuffix(request.URL.Path, "/app"):
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := writer.Write([]byte(nativeIdentityPage(gaia))); err != nil {
				t.Error(err)
			}
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	service.webOriginOverride = server.URL
	service.webRotateOverride = server.URL
	return server
}
