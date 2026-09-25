package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStoreFencesRestart_whenDurabilityOperationFails(t *testing.T) {
	for _, point := range []string{"file_sync", "rename", "directory_sync"} {
		t.Run(point, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "sessions")
			store, err := openSessionStore(directory, sessionKeyFixture())
			if err != nil {
				t.Fatal(err)
			}
			record := localRecordFixture(t)
			if err := store.write(record); err != nil {
				t.Fatal(err)
			}
			store.fault = func(operation string) error {
				if operation == point {
					return errors.New("fixture I/O failure")
				}
				return nil
			}
			record.State = localRenewing

			err = store.write(record)

			if err == nil {
				t.Fatal("fault reported success")
			}
			if _, err := store.read(record.Target.TokenRef); err == nil {
				t.Fatal("uncertain store stayed usable")
			}
			if err := store.close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := openSessionStore(directory, sessionKeyFixture()); err == nil {
				t.Error("restart cleared durability fence")
				if err := reopened.close(); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestLocalStoreRejectsTampering_whenEnvelopeOrPathChanged(t *testing.T) {
	for _, field := range []string{"account", "reference", "identity", "revision", "state", "schema", "purpose", "nonce", "ciphertext", "symlink", "oversized"} {
		t.Run(field, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "sessions")
			store, err := openSessionStore(directory, sessionKeyFixture())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.close(); err != nil {
					t.Error(err)
				}
			})
			record := localRecordFixture(t)
			if err := store.write(record); err != nil {
				t.Fatal(err)
			}
			name, err := sessionFile(record.Target.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var envelope sessionEnvelope
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "account":
				envelope.AAD.AccountID = "gemini-web-b.json"
			case "reference":
				envelope.AAD.Reference = "session://gemini-web/" + strings.Repeat("c", 32)
			case "identity":
				envelope.AAD.Identity.AuthUser++
			case "revision":
				envelope.AAD.Revision++
			case "state":
				envelope.AAD.State = localReady
			case "schema":
				envelope.AAD.Schema++
			case "purpose":
				envelope.AAD.Purpose = "other"
			case "nonce":
				envelope.Nonce[0] ^= 1
			case "ciphertext":
				envelope.Ciphertext[0] ^= 1
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, make([]byte, 128*1024+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if field != "symlink" && field != "oversized" {
				if err := os.WriteFile(path, jsonFixture(t, envelope), 0600); err != nil {
					t.Fatal(err)
				}
			}

			_, err = store.read(record.Target.TokenRef)

			if err == nil {
				t.Fatal("tampered record accepted")
			}
		})
	}
}

func TestLocalMaintenanceRenewsWithoutCDPOrVault_whenNoLegacySourceConfigured(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)

	result, err := service.maintain(t.Context(), managementRequest{HostCallbackID: "fixture-maintain", Body: jsonFixture(t, struct {
		ID string `json:"id"`
	}{ready.AccountID})})

	if err != nil || len(result.Results) != 1 || result.Results[0].State != maintenanceReady {
		t.Fatalf("maintenance result=%+v err=%v", result, err)
	}
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil || record.SessionRevision != 2 {
		t.Fatal("local maintenance did not persist exactly one local revision")
	}
}

func TestLocalRestartRetainsIntentFence_whenOperationWasAmbiguous(t *testing.T) {
	for _, state := range []localState{localRenewing, localSubmitting} {
		t.Run(string(state), func(t *testing.T) {
			service, host := loginFixture(t)
			started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
			ready := completeFixture(t, service, started)
			record, err := service.parseStorage(host.records[ready.AccountID], true)
			if err != nil {
				t.Fatal(err)
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			local.State = state
			if err := service.localStore().write(local); err != nil {
				t.Fatal(err)
			}
			if err := service.localStore().close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := openSessionStore(service.settings().SessionDir, sessionKeyFixture())
			if err != nil {
				t.Fatal(err)
			}
			service.sessions = reopened
			service.leases = credentialLeases{}

			_, _, err = service.resolve(t.Context(), host.records[ready.AccountID], ready.AccountID)

			if err == nil || safeCredentialCode(err) != "needs_operator" {
				t.Fatalf("intent did not block restart: %v", err)
			}
		})
	}
}

func TestLoginPreservesStableIDAndDisabled_whenRelinkingAnExistingAccount(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			service, host := loginFixture(t)
			previous := recordFixture(t, "a")
			previous.SessionRevision = 8
			host.records[previous.ID] = jsonFixture(t, previous)
			host.disabled = disabled
			seedSession(t, service, previous, sessionToken{encodedToken("test-existing")})
			oldLease := service.leases.get(previous.TokenRef)
			started, status := loginCall(t, service, "start", jsonFixture(t, struct {
				Label      string `json:"label"`
				ExistingID string `json:"existing_id"`
				Consent    bool   `json:"consent"`
			}{"Relogin", previous.ID, true}))
			if status != 200 || started.ExpectedIdentity == nil {
				t.Fatalf("start status=%d error=%s", status, started.Error)
			}

			view := completeFixture(t, service, started)

			if view.AccountID != previous.ID || (view.Status != loginReady && view.Status != loginSaved) || view.ModelsReady == disabled {
				t.Fatalf("view=%+v", view)
			}
			record, err := service.parseStorage(host.records[previous.ID], true)
			if err != nil || record.Disabled != disabled || record.SessionRevision != 9 {
				t.Fatal("migration changed host-owned identity/disabled/revision")
			}
			if service.leases.get(record.TokenRef) != oldLease {
				t.Fatal("migration created independent writer lease")
			}
			if _, _, err := service.resolve(t.Context(), jsonFixture(t, previous), previous.ID); err == nil {
				t.Fatal("stale legacy record resolved after migration")
			}
		})
	}
}

func TestLoginRejectsMismatchedHandoff_whenIdentityObservationDiffers(t *testing.T) {
	for _, mismatch := range []string{"gaia", "token_index", "consent", "extension"} {
		t.Run(mismatch, func(t *testing.T) {
			service, host := loginFixture(t)
			started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
			user := uint64(2)
			body := loginCompletion{State: started.State, Token: encodedToken("test-login"), AccountSHA256: testAccountDigest, AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true}
			switch mismatch {
			case "gaia":
				body.AccountSHA256 = strings.Repeat("c", 64)
			case "token_index":
				user = 3
			case "consent":
				body.Consent = false
			case "extension":
				body.ExtensionID = strings.Repeat("p", 32)
			}

			view, status := loginCall(t, service, "complete", jsonFixture(t, body))

			if status == 200 && view.Status != loginError {
				t.Fatalf("mismatch accepted: %s", view.Status)
			}
			if host.saves != 0 {
				t.Fatal("mismatched login wrote credentials")
			}
		})
	}
}

func TestLocalReadOnlyFailureDoesNotFenceSubmission_whenInspectTransportFails(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	service.client.Transport = localFailTransport{}

	_, err = service.renewLocalSession(t.Context(), "fixture-renew", record)

	if err == nil {
		t.Fatal("transport failure hidden")
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.State != localReady {
		t.Fatal("read-only inspection marked an unknown submission")
	}
}

type localFailTransport struct{}

func (localFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.Canceled
}

func TestSessionShutdownDrainsCallsAndReleasesLock_whenWorkCompletes(t *testing.T) {
	service, _ := loginFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	service.host = func(string, []byte) ([]byte, error) {
		close(entered)
		<-release
		return []byte(`{"ok":true,"result":{"files":[]}}`), nil
	}
	finished := make(chan []byte, 1)
	go func() {
		finished <- service.handle(context.Background(), "management.handle", jsonFixture(t, managementRequest{Method: "GET", Path: accountsPath, HostCallbackID: "fixture-drain"}))
	}()
	<-entered
	shutdown := make(chan error, 1)
	go func() { shutdown <- service.shutdownSessions() }()
	<-service.lifecycle.closingSignal()
	if other, err := openSessionStore(service.settings().SessionDir, sessionKeyFixture()); err == nil {
		t.Error("lock released before drain")
		if err := other.close(); err != nil {
			t.Error(err)
		}
	}
	close(release)
	<-finished

	err := <-shutdown

	if err != nil {
		t.Fatal(err)
	}
	if result := invoke(t, service, "management.register", struct{}{}); result.OK {
		t.Fatal("shutdown admitted new work")
	}
	reopened, err := openSessionStore(service.settings().SessionDir, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.close(); err != nil {
		t.Fatal(err)
	}
}

func TestLoginReconcileVerifiesHostAgain_whenReadyRecordSurvivedRestart(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	delete(host.records, ready.AccountID)
	service.logins = nil

	view, status := loginCall(t, service, "reconcile", jsonFixture(t, struct {
		State string `json:"state"`
	}{started.State}))

	if status != 200 || view.Status != loginReady || host.saves != 2 {
		t.Fatalf("recovery failed status=%d view=%s saves=%d", status, view.Status, host.saves)
	}
}

func TestLoginStatusDoesNotClaimReadiness_whenSupportedModelsAreAbsent(t *testing.T) {
	service, _ := loginFixture(t)
	base := service.client.Transport
	service.client.Transport = accountModelsOverride{base: base}
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))

	view := completeFixture(t, service, started)

	if view.Status != loginSaved || view.ModelsReady {
		t.Fatalf("unsupported data claimed ready: %s", view.Status)
	}
}

type accountModelsOverride struct{ base http.RoundTripper }

func (transport accountModelsOverride) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/v1/account-models" {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"available":true,"models":[{"capability_id":"future","display_name":"Unknown"}]}`))}, nil
	}
	return transport.base.RoundTrip(request)
}

func TestLoginRejectsDifferentReplayWithoutErasingCommit_whenStateAlreadySaved(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	user := uint64(2)

	view, status := loginCall(t, service, "complete", jsonFixture(t, loginCompletion{State: started.State, Token: encodedToken("different-token"), AccountSHA256: testAccountDigest, AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true}))

	if status != 409 || view.Error != "login_replay_mismatch" || host.saves != 1 || len(host.records[ready.AccountID]) == 0 {
		t.Fatalf("replay status=%d error=%s", status, view.Error)
	}
}

func TestLoginStatusRequiresFreshHostEvidence_whenHostProjectionDisappears(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	delete(host.records, ready.AccountID)

	view, status := loginCall(t, service, "status", jsonFixture(t, struct {
		State string `json:"state"`
	}{started.State}))

	if status != 200 || view.ModelsReady || view.Status != loginHostPending || host.saves != 1 {
		t.Fatalf("stale readiness status=%d view=%s ready=%t saves=%d", status, view.Status, view.ModelsReady, host.saves)
	}
}

func TestLocalStoreKeyConfigurationRejectsConfigEmbeddedKey_whenRegistering(t *testing.T) {
	service := newService(nil)

	result := invoke(t, service, "plugin.register", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{[]byte("session_key: forbidden-in-config\n")})

	if result.OK {
		t.Fatal("config-embedded key accepted")
	}
}

func TestLocalMaintenanceReusesTargetRevision_whenHostSaveNeverArrived(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	base := service.host
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.save" {
			return nil, errors.New("fixture rejected before save")
		}
		return base(method, raw)
	}
	request := managementRequest{HostCallbackID: "fixture-maintain", Body: jsonFixture(t, struct {
		ID string `json:"id"`
	}{ready.AccountID})}
	first, err := service.maintain(t.Context(), request)
	if err != nil || first.Results[0].State != maintenanceHostPending {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	service.host = base

	second, err := service.maintain(t.Context(), request)

	if err != nil || second.Results[0].State != maintenanceReady {
		t.Fatalf("retry=%+v err=%v", second, err)
	}
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil || record.SessionRevision != 2 {
		t.Fatal("reconcile incremented revision again")
	}
}

func TestLoginReconcileRestoresCapturedCanonicalPolicy_whenHostRevisionMatchesButPolicyIsMissing(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	host.records[record.ID] = jsonFixture(t, record)

	view, status := loginCall(t, service, "reconcile", jsonFixture(t, struct {
		State string `json:"state"`
	}{started.State}))

	if status != 200 || view.Status != loginReady || host.saves != 2 {
		t.Fatalf("canonical repair=%s status=%d saves=%d", view.Status, status, host.saves)
	}
	canonical, _, err := service.canonicalHostRecord("fixture-policy", record.ID)
	if err != nil || canonical.SessionRevision != 1 {
		t.Fatal("policy repair incremented token revision")
	}
}

func TestLoginCapacityIncludesDurablePendingFlow_whenFreshServiceRegisters(t *testing.T) {
	service, host := loginFixture(t)
	host.loseResponse = true
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	if flow := completeFixture(t, service, started); flow.Status != loginHostPending {
		t.Fatal("fixture did not retain pending host sync")
	}
	config := service.settings()
	if err := service.localStore().close(); err != nil {
		t.Fatal(err)
	}
	restarted := newService(host.call)
	host.service = restarted
	yaml := fmt.Sprintf("session_dir: %s\nmanager_origin: %s\nbrowser_extension_id: %s\n", config.SessionDir, config.ManagerOrigin, config.BrowserExtensionID)
	result := invoke(t, restarted, "plugin.register", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{[]byte(yaml)})
	if !result.OK {
		t.Fatalf("restart: %+v", result.Error)
	}
	t.Cleanup(func() {
		if err := restarted.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	for range 31 {
		if _, status := loginCall(t, restarted, "start", []byte(`{"label":"Fixture","consent":true}`)); status != 200 {
			t.Fatalf("early capacity failure: %d", status)
		}
	}

	_, status := loginCall(t, restarted, "start", []byte(`{"label":"Fixture","consent":true}`))

	if status != 429 {
		t.Fatal("restart forgot durable active login")
	}
}
