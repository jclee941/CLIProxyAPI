package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

type sessionModelObserver struct {
	base    http.RoundTripper
	observe func() error
}

func (observer sessionModelObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/v1/account-models" {
		if err := observer.observe(); err != nil {
			return nil, err
		}
	}
	return observer.base.RoundTrip(request)
}

func TestLocalModelsUseCommittedRevision_whenHostSaveMarkerClosedBeforeWriterRelease(t *testing.T) {
	for _, phase := range []localState{localHostPending, localReady} {
		t.Run(string(phase), func(t *testing.T) {
			service, _ := loginFixture(t)
			started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
			base := service.host
			var target storageRecord
			saved, observed := false, false
			observe := func() error {
				if !saved || observed {
					return nil
				}
				local, err := service.localStore().read(target.TokenRef)
				if err != nil {
					return err
				}
				if local.State != phase {
					return nil
				}
				observed = true
				service.loginMu.Lock()
				pending := service.modelPending[target.ID]
				service.loginMu.Unlock()
				if pending != 0 {
					t.Error("fixture did not close host-save marker")
				}
				lease := service.leases.get(target.TokenRef)
				if lease.guard.TryRLock() {
					lease.guard.RUnlock()
					t.Error("fixture did not retain writer guard")
				}
				result := invoke(t, service, "model.for_auth", struct {
					AuthID, AuthProvider string
					StorageJSON          []byte
				}{target.ID, provider, jsonFixture(t, target)})
				if !result.OK {
					t.Errorf("exact committed revision discovery rejected: %+v", result.Error)
					return nil
				}
				var models struct{ Models []modelInfo }
				if err := json.Unmarshal(result.Result, &models); err != nil {
					return err
				}
				if len(models.Models) != 1 || models.Models[0].ID != interactionOmniModel {
					t.Error("discovery did not return fresh supported models")
				}
				return nil
			}
			service.host = func(method string, raw []byte) ([]byte, error) {
				if method == "host.auth.list" {
					if err := observe(); err != nil {
						return nil, err
					}
				}
				response, err := base(method, raw)
				if method == "host.auth.save" && err == nil {
					var request callbackRequest
					if err := json.Unmarshal(raw, &request); err != nil {
						return nil, err
					}
					target, err = service.parseStorage(request.JSON, true)
					saved = err == nil
				}
				return response, err
			}
			service.client.Transport = sessionModelObserver{base: service.client.Transport, observe: observe}

			view := completeFixture(t, service, started)

			if view.Status != loginReady || !observed {
				t.Fatalf("writer did not finish read-only callback: status=%s observed=%t", view.Status, observed)
			}
		})
	}
}

// Reading the capabilities is itself a call Google rotates the cookie on, and
// the plugin stores that rotation as it happens. Treating the fresher token as a
// swapped credential published no models at all, which took the whole fleet out
// of routing while every account was healthy.
func TestLocalModelsSurviveACookieRotationDuringDiscovery(t *testing.T) {
	service, _ := loginFixture(t)
	local := localRecordFixture(t)
	local.State = localReady
	store := service.localStore()
	if err := store.write(local); err != nil {
		t.Fatal(err)
	}
	service.client.Transport = sessionModelObserver{base: service.client.Transport, observe: func() error {
		local.Token = encodedToken("rotated-during-discovery")
		return store.write(local)
	}}

	models, err := service.localAuthModels(t.Context(), local.Target)

	if err != nil {
		t.Fatalf("a rotation during discovery was read as a credential change: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("a rotation during discovery published no models")
	}
}

func TestLocalModelsDiscardEntitlements_whenSnapshotChangesDuringHTTP(t *testing.T) {
	// "token" is deliberately absent: a token that changes without the revision
	// moving is Google rotating the cookie on the very call being made, which is
	// the good outcome and is covered by the test below.
	// "revision" and "state" are deliberately absent alongside "token": a renewal
	// bumping the revision and parking the session in host_sync_pending is the
	// account preparing to generate, and a turn finishing moves it back. Those are
	// the account working, not a credential being swapped, and refusing them cost
	// production 315 discovery failures whose only effect was to drop healthy
	// accounts out of the host's candidates. They are covered as allowed in
	// session_discovery_race_test.go.
	for _, change := range []string{"revision", "renewal_intent", "submission_intent", "durability", "operator", "fence"} {
		t.Run(change, func(t *testing.T) {
			service, _ := loginFixture(t)
			local := localRecordFixture(t)
			local.State = localReady
			store := service.localStore()
			if err := store.write(local); err != nil {
				t.Fatal(err)
			}
			record := local.Target
			wantError := "credential_changed"
			service.client.Transport = sessionModelObserver{base: service.client.Transport, observe: func() error {
				switch change {
				case "revision":
					local.Target.SessionRevision++
					auth, err := authFromRecord(local.Target)
					if err != nil {
						return err
					}
					local.Projection = string(auth.StorageJSON)
				case "token":
					local.Token = encodedToken("changed-during-discovery")
				case "state":
					local.State = localHostPending
				case "renewal_intent":
					local.State = localRenewing
					wantError = "needs_operator"
				case "submission_intent":
					local.State = localSubmitting
					wantError = "needs_operator"
				case "durability":
					store.fault = func(string) error { return errors.New("fixture durability failure") }
					if err := store.write(local); err == nil {
						t.Error("fixture failed to fence store")
					}
					wantError = "session_store_unavailable"
					return nil
				case "operator":
					service.leases.get(record.TokenRef).set(credentialState{state: maintenanceOperator})
					wantError = "needs_operator"
					return nil
				case "fence":
					service.leases.get(record.TokenRef).set(credentialState{state: maintenanceFenced, nextDue: service.now().Add(credentialFenceDuration)})
					wantError = "fenced"
					return nil
				}
				return store.write(local)
			}}

			models, err := service.localAuthModels(t.Context(), record)

			if err == nil || safeCredentialCode(err) != wantError || len(models) != 0 {
				t.Fatalf("changed snapshot published models: count=%d err=%v want=%s", len(models), err, wantError)
			}
		})
	}
}

func TestLocalModelsRejectUncertainSnapshotBeforeHTTP_whenCommittedStateIsUnsafe(t *testing.T) {
	for _, state := range []localState{localRenewing, localSubmitting} {
		t.Run(string(state), func(t *testing.T) {
			service, _ := loginFixture(t)
			local := localRecordFixture(t)
			local.State = state
			if err := service.localStore().write(local); err != nil {
				t.Fatal(err)
			}
			service.client.Transport = sessionModelObserver{base: service.client.Transport, observe: func() error {
				t.Error("uncertain credential reached upstream discovery")
				return errors.New("unexpected model request")
			}}

			models, err := service.localAuthModels(t.Context(), local.Target)

			if err == nil || safeCredentialCode(err) != "needs_operator" || len(models) != 0 {
				t.Fatalf("unsafe snapshot published models: count=%d err=%v", len(models), err)
			}
		})
	}
}
