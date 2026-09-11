package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMaintenanceRejectsBadRequests_beforeUpstreamOrHostLookup(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"id":3}`, `{"id":""}`, `{"id":"unknown"}`, `{"id":"gemini-web-a.json","extra":true}`, `{"id":"gemini-web-a.json","id":"gemini-web-b.json"}`} {
		t.Run(body, func(t *testing.T) {
			service, store, _, _ := maintenanceFixture(t)
			var callbacks atomic.Int32
			service.host = func(string, []byte) ([]byte, error) { callbacks.Add(1); return nil, nil }
			localSidecar(t, service, func(http.ResponseWriter, *http.Request) { t.Error("invalid body reached HTTP") })

			response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: "/v0/management" + maintainPath, HostCallbackID: "scope-list", Body: []byte(body)}))

			if err != nil || response.StatusCode < 400 || callbacks.Load() != 0 || len(store.reads) != 0 {
				t.Fatal("invalid request had side effects")
			}
		})
	}
}

func TestMaintenanceSelectsConfiguredRegisteredOnly_whenInvokedWithoutID(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	second := recordFixture(t, "b")
	service.host = accountHost(t, []storageRecord{*record, second})
	unregistered := testMaintenanceSource()
	unregistered.TokenRef = recordFixture(t, "c").TokenRef
	unregistered.ProfileGUID = "22222222-2222-2222-2222-222222222222"
	unregistered.ExpectedGaiaSHA256 = strings.Repeat("c", 64)
	service.config.MaintenanceSources["gemini-web-c.json"] = unregistered
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("original")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})

	result := maintainFixture(t, service, `{}`)

	if len(result.Results) != 1 || result.Results[0].ID != record.ID || len(store.reads) != 1 || store.reads[0] != record.TokenRef || store.writes != 0 {
		t.Fatal("maintenance enumerated unbound or unregistered credentials")
	}
}

func TestManualBoundReplacementRejectsRebinding_beforeSecretWrite(t *testing.T) {
	for _, scenario := range []string{"reference", "hash", "index"} {
		t.Run(scenario, func(t *testing.T) {
			service, store, record, saves := maintenanceFixture(t)
			body := struct {
				Label      string `json:"label"`
				Token      string `json:"token,omitempty"`
				TokenRef   string `json:"token_ref,omitempty"`
				ExistingID string `json:"existing_id"`
			}{Label: "Update", Token: encodedToken("manual"), ExistingID: record.ID}
			if scenario == "reference" {
				body.Token = ""
				body.TokenRef = recordFixture(t, "b").TokenRef
			}
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/v1/session/inspect" {
					t.Error("invalid manual identity reached models")
				}
				identity := credentialInspection{strings.Repeat("a", 64), 2}
				if scenario == "hash" {
					identity.AccountSHA256 = strings.Repeat("b", 64)
				}
				if scenario == "index" {
					identity.AuthUser = 0
				}
				writeFixture(t, writer, string(jsonFixture(t, identity)))
			})

			response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-list", Body: jsonFixture(t, body)}))

			if err != nil || response.StatusCode != 409 || !strings.Contains(string(response.Body), "binding_mismatch") || store.writes != 0 || saves.Load() != 0 {
				t.Fatalf("bound manual token rebound: %v %s", err, response.Body)
			}
		})
	}
}

func TestManualVerifiedReplacementResetsCooldown_whenTokenIsFresh(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	service.rejectMaintenance(record.ID, store.tokens[record.TokenRef], failure(401, "source_login_required"))
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/inspect" {
			writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		writeFixture(t, writer, `{"available":true,"models":[]}`)
	})

	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		Label, Token string
		ExistingID   string `json:"existing_id"`
	}{"Update", encodedToken("manual"), record.ID})}))

	if err != nil || response.StatusCode != 200 || store.writes != 1 || saves.Load() != 1 || service.leases.get(record.TokenRef).snapshot().state != maintenanceReady {
		t.Fatalf("verified manual update failed: %v %s", err, response.Body)
	}
}

func TestMaintenanceRegistrationAdvertisesAuthenticatedRouteAndObjectConfig(t *testing.T) {
	service := newService(nil)
	registration := invoke(t, service, "plugin.register", struct{}{})
	var manifest struct {
		Metadata struct{ ConfigFields []struct{ Name, Type string } }
	}
	if !registration.OK || json.Unmarshal(registration.Result, &manifest) != nil {
		t.Fatal("registration failed")
	}
	found := false
	for _, field := range manifest.Metadata.ConfigFields {
		if field.Name == "maintenance_sources" && field.Type == "object" {
			found = true
		}
	}
	if !found {
		t.Fatal("source bindings missing from object config metadata")
	}
	registered := invoke(t, service, "management.register", struct{}{})
	var management struct {
		Routes    []struct{ Method, Path string }
		Resources []struct{ Path string }
	}
	if !registered.OK || json.Unmarshal(registered.Result, &management) != nil {
		t.Fatal("management registration failed")
	}
	found = false
	for _, route := range management.Routes {
		if route.Method == "POST" && route.Path == maintainPath {
			found = true
		}
	}
	if !found || len(management.Resources) != 1 || management.Resources[0].Path != "/index" {
		t.Fatal("maintenance was not authenticated management-only")
	}
}

func TestAuthenticationFallbackRejectsAmbiguousErrorDTO_whenMessageDuplicated(t *testing.T) {
	service, store, _, saves := maintenanceFixture(t)
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(401)
		writeFixture(t, writer, `{"error":{"message":"bootstrap_failed","message":"auth_error"}}`)
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceCredentialError || store.writes != 0 || saves.Load() != 0 {
		t.Fatal("ambiguous auth response triggered capture")
	}
}
