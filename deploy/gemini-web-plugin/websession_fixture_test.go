package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The account identity is derived from the page rather than reported by a
// bridge, so tests must serve a page that yields the digest they assert on.
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
	return encodeWebCredential(cookie, authUser).value
}

// sidecarPath maps the native credential routes onto the bridge names the tests
// were written against, so a switch over them keeps expressing the same intent
// after the upkeep calls moved to the account page and the rotation endpoint.
func sidecarPath(request *http.Request) string {
	switch {
	case strings.HasSuffix(request.URL.Path, "/app"):
		return "/v1/session/inspect"
	case request.URL.Path == "/RotateCookies":
		return "/v1/session/renew"
	}
	return request.URL.Path
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
	return encodeWebCredential(webMergeCookies(cookie, map[string]string{rotatingCookie: "rotated"}), 2).value
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
