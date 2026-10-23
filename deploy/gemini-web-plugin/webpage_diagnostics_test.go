package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplicationPageFailureReportsStructureWithoutSecrets(t *testing.T) {
	for _, identity := range []bool{false, true} {
		t.Run(map[bool]string{false: "bootstrap", true: "identity"}[identity], func(t *testing.T) {
			page := `window.WIZ_global_data={"SNlM0e":"xsrf-secret","cfb2h":"","S06Grb":"private-account"}; private-html`
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/app" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				http.SetCookie(w, &http.Cookie{Name: "SIDCC", Value: "cookie-secret", Path: "/", Domain: ".google.com"})
				writeFixture(t, w, page)
			}))
			t.Cleanup(server.Close)
			var reports [][]byte
			service := newService(func(method string, raw []byte) ([]byte, error) {
				if method != "host.log" {
					t.Errorf("unexpected callback: %s", method)
				}
				reports = append(reports, append([]byte(nil), raw...))
				return []byte(`{}`), nil
			})
			service.webOriginOverride = server.URL
			credential := webCredential{Cookie: "SID=credential-secret"}
			code := "web_bootstrap_failed"
			var err error
			if identity {
				code = "credential_identity_invalid"
				_, err = service.webIdentity(t.Context(), "", credential)
			} else {
				session := service.newSession(credential)
				service.trackJar("", session)
				err = session.bootstrap(t.Context())
			}
			var public *publicError
			if !errors.As(err, &public) || public.Code != code || public.HTTPStatus != 502 {
				t.Fatalf("failure contract changed: %v", err)
			}
			if calls.Load() != 1 || len(reports) != 1 {
				t.Fatalf("requests=%d reports=%d", calls.Load(), len(reports))
			}
			var report struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.Unmarshal(reports[0], &report); err != nil {
				t.Fatal(err)
			}
			if report.Fields["error"] != code || report.Fields["state"] != "app_page_invalid" ||
				report.Fields["reason"] != "xsrf=true build=false session=false wiz_count=1 identity=false" {
				t.Fatalf("unexpected structural diagnostic: %+v", report.Fields)
			}
			for _, secret := range []string{"xsrf-secret", "private-account", "private-html", "cookie-secret", "credential-secret"} {
				if strings.Contains(string(reports[0]), secret) {
					t.Fatal("diagnostic leaked fixture secret")
				}
			}
		})
	}
}
