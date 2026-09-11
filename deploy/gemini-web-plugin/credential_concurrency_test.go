package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type pausedSecrets struct {
	*memorySecrets
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (store *pausedSecrets) Resolve(ctx context.Context, reference secretReference) (sessionToken, error) {
	store.once.Do(func() { close(store.entered); <-store.release })
	return store.memorySecrets.Resolve(ctx, reference)
}

func TestGenerationHoldsCredentialLease_whenResolvingOrGenerating(t *testing.T) {
	for _, model := range []string{flashModel, omniModel} {
		for _, phase := range []string{"resolve", "generate"} {
			t.Run(model+"/"+phase, func(t *testing.T) {
				service, store, record, saves := maintenanceFixture(t)
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				if phase == "resolve" {
					service.secrets = &pausedSecrets{memorySecrets: store, entered: entered, release: release}
				}
				var upstream atomic.Int32
				localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
					upstream.Add(1)
					switch request.URL.Path {
					case "/v1/session/inspect":
						writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
					case "/v1/session/renew":
						writeFixture(t, writer, `{"token":"`+encodedToken("original")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
					case "/v1/account-models":
						writeFixture(t, writer, `{"available":true,"models":[{"capability_id":"flash","display_name":"3.8 Flash"}]}`)
					default:
						if phase == "generate" {
							close(entered)
							<-release
						}
						if model == omniModel {
							writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"dGVzdA=="}}]}}]}`)
						} else {
							writeFixture(t, writer, `{"candidates":[]}`)
						}
					}
				})
				auth, err := authFromRecord(*record)
				if err != nil {
					t.Fatal(err)
				}
				raw := jsonFixture(t, executorRequest{AuthID: record.ID, AuthProvider: provider, Model: model, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"generate"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})
				finished := make(chan []byte, 1)
				go func() { finished <- service.handle(t.Context(), "executor.execute", raw) }()
				t.Cleanup(unblock)
				<-entered
				before := upstream.Load()

				result := maintainFixture(t, service, `{}`)
				response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
					Label, Token string
					ExistingID   string `json:"existing_id"`
				}{"Updated", encodedToken("new"), record.ID})}))

				if result.Results[0].State != maintenanceBusy || err != nil || response.StatusCode != 409 || upstream.Load() != before || saves.Load() != 0 {
					t.Fatal("writer escaped active generation lease")
				}
				unblock()
				var execution envelope
				if err := json.Unmarshal(<-finished, &execution); err != nil || !execution.OK {
					t.Fatalf("generation failed: %v %+v", err, execution.Error)
				}
				if store.writes != 0 {
					t.Fatal("busy generation triggered credential write")
				}
			})
		}
	}
}

func TestHostSaveAllowsReadonlyModelCallback_whenMaintenanceLeaseHeld(t *testing.T) {
	service, _, record, _ := maintenanceFixture(t)
	host := service.host
	var modelCallbacks atomic.Int32
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.save" {
			auth, err := authFromRecord(*record)
			if err != nil {
				t.Fatal(err)
			}
			result := invoke(t, service, "model.for_auth", struct {
				AuthID, AuthProvider string
				StorageJSON          []byte
			}{record.ID, provider, auth.StorageJSON})
			if !result.OK {
				t.Error("readonly model callback rejected")
			}
			modelCallbacks.Add(1)
		}
		return host(method, raw)
	}
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/session/inspect":
			writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
		case "/v1/session/renew":
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
		case "/v1/account-models":
			writeFixture(t, writer, `{"available":true,"models":[]}`)
		default:
			t.Error("unexpected sidecar call")
		}
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceReady || modelCallbacks.Load() != 1 {
		t.Fatal("host callback did not complete synchronously")
	}
}

func TestReconfigurationRejectsBindingChanges_whenReferenceBusy(t *testing.T) {
	service, _, record, _ := maintenanceFixture(t)
	lease, err := service.acquireCredential(record.TokenRef, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.guard.RUnlock()
	config := []byte("maintenance_sources: {}\n")

	result := invoke(t, service, "plugin.reconfigure", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{config})

	if result.OK || result.Error.Code != "session_busy" || len(service.settings().MaintenanceSources) != 1 || service.leases.get(record.TokenRef) != lease {
		t.Fatal("reconfiguration replaced a busy reference guard")
	}
}

func TestFlashSharesCredentialLease_whenAnotherReaderActive(t *testing.T) {
	service := newService(nil)
	reference := testMaintenanceSource().TokenRef
	first, err := service.acquireCredential(reference, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.guard.RUnlock()

	second, err := service.acquireCredential(reference, false)

	if err != nil {
		t.Fatal("Flash readers were serialized")
	}
	second.guard.RUnlock()
}
