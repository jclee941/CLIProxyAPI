package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// A submission that carried no frame at all is the case the report exists for,
// so it must not be the case the report stays quiet about.
func TestASubmissionThatCarriedNoFrameIsStillReported(t *testing.T) {
	var reported []byte
	service := newService(func(method string, raw []byte) ([]byte, error) {
		if method == "host.log" {
			reported = raw
		}
		return []byte(`{"ok":true,"result":{}}`), nil
	})

	service.reportUnnamed("missing_upstream_operation", continuationTurn{}, 0, nil)

	if reported == nil {
		t.Fatal("a submission with nothing to show reported nothing")
	}
	var entry struct {
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(reported, &entry); err != nil {
		t.Fatal(err)
	}
	if budget, _ := entry.Fields["budget"].(string); !strings.Contains(budget, "0 frames from 0 lines") {
		t.Fatalf("budget = %q, want the emptiness spelled out", budget)
	}
}

// A cut stream is the only record of who ended a generation early, and the host
// log is where an operator reads. The names are the host formatter's own allow
// list in internal/logging/global_logger.go: anything else is dropped before it
// is written, so a report can arrive and still say nothing.
func TestACutGenerationStreamReachesTheHostLog(t *testing.T) {
	var reported []byte
	service := newService(func(method string, raw []byte) ([]byte, error) {
		if method == "host.log" {
			reported = raw
		}
		return []byte(`{"ok":true,"result":{}}`), nil
	})
	delivered := `[["di",1]]` + "\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(delivered)+64))
		_, _ = writer.Write([]byte(delivered))
	}))
	defer server.Close()
	service.webOriginOverride = server.URL
	session := service.newSession(webCredential{Cookie: "SID=x"})
	session.generationFrame = func([]byte) error { return nil }

	_, err := session.do(context.Background(), webGeneratePath, []byte("{}"), nil)

	if safeCredentialCode(err) != "web_response_failed" {
		t.Fatalf("code = %q, want the failure callers already handle", safeCredentialCode(err))
	}
	if reported == nil {
		t.Fatal("the cut was never reported to the host")
	}
	var entry struct {
		Level  string         `json:"level"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(reported, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Level != "warn" {
		t.Fatalf("level = %q, want one the deployed log keeps", entry.Level)
	}
	rendered := map[string]bool{"provider": true, "state": true, "reason": true, "error": true, "budget": true, "remote_transport": true}
	for name := range entry.Fields {
		if !rendered[name] {
			t.Fatalf("field %q is not one the host formatter renders", name)
		}
	}
	if entry.Fields["error"] != "web_response_failed" {
		t.Fatalf("error = %v, want the public code", entry.Fields["error"])
	}
	budget, _ := entry.Fields["budget"].(string)
	if !strings.Contains(budget, strconv.Itoa(len(delivered))) || !strings.Contains(budget, strconv.Itoa(len(delivered)+64)) {
		t.Fatalf("budget = %q, want what arrived against what the response declared", budget)
	}
}

// slots builds one of the protocol's positional rows: a fixed-width array whose
// meaning is carried entirely by index.
func slots(size int, values map[int]any) []any {
	row := make([]any, size)
	for index, value := range values {
		row[index] = value
	}
	return row
}

// rpcEnvelope renders the batchexecute framing: the hijacking prefix, then a
// length line counting UTF-16 code units, then the frame itself.
func rpcEnvelope(t *testing.T, rpcID string, payload any) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	frame, err := json.Marshal([]any{[]any{"wrb.fr", rpcID, string(encoded), nil, nil, nil, "generic"}})
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	return ")]}'\n\n" + fmt.Sprintf("%d\n%s\n", len(utf16.Encode([]rune(string(frame)))), frame)
}

func TestDecodeWebCredentialReadsTheCookie(t *testing.T) {
	raw, err := json.Marshal(webCredential{Cookie: "SID=a; SAPISID=b", AuthUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := decodeWebCredential(sessionToken{"gemini-web:v1:" + base64.RawURLEncoding.EncodeToString(raw)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if credential.Cookie != "SID=a; SAPISID=b" || credential.AuthUser != 2 {
		t.Fatalf("credential = %+v", credential)
	}
	for _, broken := range []string{"", "gemini-web:v2:abc", "gemini-web:v1:!!!", "gemini-web:v1:" + base64.RawURLEncoding.EncodeToString([]byte(`{"auth_user":2}`))} {
		if _, err := decodeWebCredential(sessionToken{broken}); err == nil {
			t.Fatalf("token %q was accepted", broken)
		}
	}
}

// The length line counts UTF-16 code units, so a reply carrying characters
// outside the basic plane must still be accepted.
func TestDecodeRPCFramesCountsUTF16CodeUnits(t *testing.T) {
	payload := []any{"안녕", "🌍"}
	decoded, err := decodeRPCFrames([]byte(rpcEnvelope(t, "otAQ7b", payload)), "otAQ7b")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first, _ := jsonField(decoded, 0).(string); first != "안녕" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestDecodeRPCFramesRejectsDeniedAndMalformedReplies(t *testing.T) {
	denial, err := json.Marshal([]any{[]any{"wrb.fr", "otAQ7b", nil, nil, nil, []any{7}, "generic"}})
	if err != nil {
		t.Fatal(err)
	}
	denied := ")]}'\n\n" + fmt.Sprintf("%d\n%s\n", len(utf16.Encode([]rune(string(denial)))), denial)
	if _, err := decodeRPCFrames([]byte(denied), "otAQ7b"); err == nil {
		t.Fatal("a rejection frame was accepted")
	}

	envelope := rpcEnvelope(t, "otAQ7b", []any{"value"})
	if _, err := decodeRPCFrames([]byte(envelope), "different"); err == nil {
		t.Fatal("a frame for another rpc was accepted")
	}
	if _, err := decodeRPCFrames([]byte(")]}'\n\n9999\n[]\n"), "otAQ7b"); err == nil {
		t.Fatal("a mismatched length was accepted")
	}
}

func webAccountFixture(t *testing.T, payload any) (*webSession, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			if _, err := writer.Write([]byte(`{"SNlM0e":"xsrf-token","cfb2h":"build-id","FdrFJe":"session-id"}`)); err != nil {
				t.Error(err)
			}
		case strings.Contains(request.URL.Path, "batchexecute"):
			if request.Header.Get("X-Same-Domain") != "1" || !strings.HasPrefix(request.Header.Get("Authorization"), "SAPISIDHASH ") {
				t.Errorf("web headers missing: %v", request.Header)
			}
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.PostForm.Get("at") != "xsrf-token" {
				t.Errorf("xsrf not carried: %q", request.PostForm.Get("at"))
			}
			if _, err := writer.Write([]byte(rpcEnvelope(t, accountCapabilityRPC, payload))); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	session := newWebSession(server.Client(), webCredential{Cookie: "SID=a; SAPISID=secret", AuthUser: 2}, server.URL)
	return session, server
}

func TestWebCapabilitiesReadsTheAccountModels(t *testing.T) {
	body := slots(16, map[int]any{
		14: float64(1000),
		15: []any{
			slots(18, map[int]any{0: "cap-flash", 11: "3.8 Flash", 17: float64(1)}),
			slots(18, map[int]any{0: "cap-pro", 11: "3.1 Pro", 17: float64(3)}),
		},
	})
	session, _ := webAccountFixture(t, body)
	account, err := session.webCapabilities(context.Background())
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(account.Capabilities) != 2 {
		t.Fatalf("capabilities = %+v", account.Capabilities)
	}
	if account.Capabilities[0] != (capability{CapabilityID: "cap-flash", DisplayName: "3.8 Flash", Mode: 1}) {
		t.Fatalf("first = %+v", account.Capabilities[0])
	}
	if account.Capabilities[1] != (capability{CapabilityID: "cap-pro", DisplayName: "3.1 Pro", Mode: 3}) {
		t.Fatalf("second = %+v", account.Capabilities[1])
	}
}

func TestWebCapabilitiesCarriesCapacityFlags(t *testing.T) {
	body := slots(17, map[int]any{
		14: float64(1000),
		15: []any{slots(18, map[int]any{0: "cap", 11: "3.8 Flash", 17: float64(1)})},
		16: []any{float64(4), float64(8)},
	})
	session, _ := webAccountFixture(t, body)
	account, err := session.webCapabilities(context.Background())
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if webCapacity(account.CapacityFlags) != 2 {
		t.Fatalf("capacity flags %v did not raise the capacity", account.CapacityFlags)
	}
	if webCapacity([]int{4}) != 1 {
		t.Fatal("capacity was raised without the flag")
	}
}

// The web app opens its /veo page only for an account whose feature list
// carries 140; the list is what tells accounts apart when the windows cannot.
func TestWebCapabilitiesReadsTheFeatureList(t *testing.T) {
	body := slots(18, map[int]any{
		14: float64(1000),
		15: []any{slots(18, map[int]any{0: "cap", 11: "3.8 Flash", 17: float64(1)})},
		16: []any{float64(4)},
		17: []any{float64(140), float64(221)},
	})
	session, _ := webAccountFixture(t, body)
	account, err := session.webCapabilities(context.Background())
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if fmt.Sprint(account.Features) != "[140 221]" || fmt.Sprint(account.CapacityFlags) != "[4]" {
		t.Fatalf("features = %v, capacity flags = %v", account.Features, account.CapacityFlags)
	}
}

// A body reporting one of these statuses carries no capability list, so the
// fixture omits that slot: supplying an empty one hid that the list was read
// first and answered web_response_invalid for a signed-out account.
func TestWebCapabilitiesSurfacesAccountStatus(t *testing.T) {
	cases := map[string]struct {
		status any
		code   string
		http   int
	}{
		"unauthenticated": {float64(1016), "web_unauthenticated", 401},
		"unavailable":     {float64(1017), "web_account_unavailable", 409},
	}
	for name, expected := range cases {
		t.Run(name, func(t *testing.T) {
			body := slots(15, map[int]any{14: expected.status})
			session, _ := webAccountFixture(t, body)
			_, err := session.webCapabilities(context.Background())
			var public *publicError
			if !errors.As(err, &public) {
				t.Fatalf("status %v answered %v", expected.status, err)
			}
			if public.Code != expected.code || public.HTTPStatus != expected.http {
				t.Fatalf("status %v answered %d %s, want %d %s", expected.status, public.HTTPStatus, public.Code, expected.http, expected.code)
			}
		})
	}
}
