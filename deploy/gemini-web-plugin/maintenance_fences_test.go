package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (transport transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func credentialResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestCredentialTransportLossFencesAllWriters_whenWorkerCompletionUnknown(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	var calls atomic.Int32
	service.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		deadline, bounded := request.Context().Deadline()
		if !bounded || time.Until(deadline) > credentialFenceDuration || time.Until(deadline) < credentialFenceDuration-time.Second {
			t.Error("credential request lost 70-second bound")
		}
		return nil, errors.New("synthetic transport loss")
	})
	first := maintainFixture(t, service, `{}`)
	if first.Results[0].State != maintenanceFenced || first.Results[0].NextDueAt != clock.Add(70*time.Second).Format(time.RFC3339) {
		t.Fatalf("missing fence: %+v", first)
	}
	clock = clock.Add(69 * time.Second)
	for _, exclusive := range []bool{true, false} {
		_, err := service.acquireCredential(record.TokenRef, exclusive)
		if safeCredentialCode(err) != "fenced" {
			t.Fatalf("fence allowed generation: %v", err)
		}
	}
	result := maintainFixture(t, service, `{}`)
	if result.Results[0].State != maintenanceFenced || calls.Load() != 1 || store.writes != 0 || saves.Load() != 0 {
		t.Fatal("fenced cycle retried")
	}
	clock = clock.Add(time.Second)
	lease, err := service.acquireCredential(record.TokenRef, true)
	if err != nil {
		t.Fatal("worker fence did not expire")
	}
	lease.guard.Unlock()
}

func TestHostPendingSurvivesFence_whenVerificationTransportLost(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	service.leases.get(record.TokenRef).set(credentialState{state: maintenanceHostPending, tokenHash: tokenFingerprint(store.tokens[record.TokenRef])})
	service.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("lost") })
	first := maintainFixture(t, service, `{}`)
	if first.Results[0].State != maintenanceFenced {
		t.Fatal("pending verification did not fence")
	}
	clock = clock.Add(70 * time.Second)
	var renewals atomic.Int32
	service.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/session/renew" {
			renewals.Add(1)
			return credentialResponse(`{"token":"` + encodedToken("rotated") + `","account_sha256":"` + strings.Repeat("a", 64) + `","auth_user":2}`), nil
		}
		return credentialResponse(`{"account_sha256":"` + strings.Repeat("a", 64) + `","auth_user":2}`), nil
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceReady || renewals.Load() != 0 || store.writes != 0 || saves.Load() != 1 {
		t.Fatal("expired fence forgot pending host sync")
	}
}

func TestCredentialTimeoutDoesNotFence_whenWorkerKilledAndWaited(t *testing.T) {
	service, store, _, saves := maintenanceFixture(t)
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(504)
		writeFixture(t, writer, `{"error":{"code":504,"message":"credential_timeout"}}`)
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceCredentialError || result.Results[0].Error != "credential_timeout" || store.writes != 0 || saves.Load() != 0 {
		t.Fatalf("worker completion proof lost: %+v", result)
	}
}

func TestHostPendingSurvivesAuthenticationCooldown_whenVerificationRejected(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	service.leases.get(record.TokenRef).set(credentialState{state: maintenanceHostPending, tokenHash: tokenFingerprint(store.tokens[record.TokenRef])})
	var healthy atomic.Bool
	var renewals, inspections atomic.Int32
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			renewals.Add(1)
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		inspections.Add(1)
		if !healthy.Load() {
			writer.WriteHeader(401)
			writeFixture(t, writer, `{"error":{"message":"auth_error"}}`)
			return
		}
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})
	maintainFixture(t, service, `{}`)
	clock = clock.Add(5 * time.Minute)
	if result := maintainFixture(t, service, `{}`); result.Results[0].State != maintenanceCooldown || inspections.Load() != 1 {
		t.Fatal("pending authentication retried during cooldown")
	}
	clock = clock.Add(25 * time.Minute)
	healthy.Store(true)

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceReady || renewals.Load() != 0 || store.writes != 0 || saves.Load() != 1 {
		t.Fatal("authentication cooldown forgot pending sync")
	}
}

func TestUnknownSecretWriteNeedsOperator_whenEditMayHaveCommitted(t *testing.T) {
	service, _, record, saves := maintenanceFixture(t)
	token := sessionToken{encodedToken("original")}
	var edits atomic.Int32
	service.secrets = opStore{vault: "homelab", run: func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		switch {
		case args[0] == "read":
			return []byte(token.value), nil
		case args[1] == "get":
			return []byte(`{"category":"SECURE_NOTE","title":"Preserve","fields":[{"id":"web-session","value":"` + token.value + `"}]}`), nil
		case args[1] == "edit":
			edits.Add(1)
			return nil, errors.New("unknown write")
		default:
			t.Error("unexpected operation")
			return nil, errors.New("denied")
		}
	}}
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})
	first := maintainFixture(t, service, `{}`)
	if first.Results[0].State != maintenanceOperator {
		t.Fatalf("unknown edit was treated as prewrite failure: %+v", first)
	}

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceOperator || edits.Load() != 1 || saves.Load() != 0 {
		t.Fatal("unknown edit automatically retried")
	}
	if _, err := service.acquireCredential(record.TokenRef, false); safeCredentialCode(err) != "needs_operator" {
		t.Fatal("unknown edit allowed submission")
	}
}

func TestUnknownOmniSubmissionNeedsOperator_withoutGenerationTimeout(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	auth, err := authFromRecord(*record)
	if err != nil {
		t.Fatal(err)
	}
	var submissions atomic.Int32
	service.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1/session/inspect" {
			return credentialResponse(`{"account_sha256":"` + strings.Repeat("a", 64) + `","auth_user":2}`), nil
		}
		if request.URL.Path == "/v1/session/renew" {
			return credentialResponse(`{"token":"` + encodedToken("original") + `","account_sha256":"` + strings.Repeat("a", 64) + `","auth_user":2}`), nil
		}
		if _, bounded := request.Context().Deadline(); bounded {
			t.Error("generation acquired a timeout")
		}
		submissions.Add(1)
		return nil, errors.New("submitted response lost")
	})
	request := executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata}
	first := invoke(t, service, "executor.execute", request)
	if first.OK {
		t.Fatal("unknown submission accepted")
	}

	second := invoke(t, service, "executor.execute", request)

	if second.OK || second.Error.Code != "gemini_web_omni:needs_operator" || submissions.Load() != 1 || store.writes != 0 {
		t.Fatal("unknown submission automatically repeated")
	}
	if result := maintainFixture(t, service, `{}`); result.Results[0].State != maintenanceOperator {
		t.Fatal("maintenance renewed unknown submission")
	}
}
