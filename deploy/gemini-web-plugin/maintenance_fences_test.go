package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (transport transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func credentialResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestCredentialTimeoutDoesNotFence_whenWorkerKilledAndWaited(t *testing.T) {
	service, _, saves := maintenanceFixture(t)
	localSidecarAll(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(504)
		writeFixture(t, writer, `{"error":{"code":504,"message":"credential_timeout"}}`)
	})

	result := maintainFixture(t, service, `{}`)

	// An upstream status is a completed answer, not an unknown outcome, so it
	// reports the failure without fencing the credential.
	if result.Results[0].State != maintenanceCredentialError || result.Results[0].Error != "web_upstream_status" || saves.Load() != 0 {
		t.Fatalf("worker completion proof lost: %+v", result)
	}
}

func TestUnknownOmniSubmissionNeedsOperator_withoutGenerationTimeout(t *testing.T) {
	service, record, _ := maintenanceFixture(t)
	auth, err := authFromRecord(*record)
	if err != nil {
		t.Fatal(err)
	}
	var submissions atomic.Int32
	service.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/app") {
			return credentialResponse(nativeIdentityPage(testGaia)), nil
		}
		if request.URL.Path == "/RotateCookies" {
			return credentialResponse(`{"token":"` + encodedToken("original") + `","account_sha256":"` + testAccountDigest + `","auth_user":2}`), nil
		}
		if _, bounded := request.Context().Deadline(); bounded {
			t.Error("generation acquired a timeout")
		}
		submissions.Add(1)
		return nil, errors.New("submitted response lost")
	})
	request := executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: "scope-list"}
	first := invoke(t, service, "executor.execute", request)
	if first.OK {
		t.Fatal("unknown submission accepted")
	}

	second := invoke(t, service, "executor.execute", request)

	if second.OK || second.Error.Code != "gemini_web_omni:needs_operator" || submissions.Load() != 1 {
		t.Fatal("unknown submission automatically repeated")
	}
	if result := maintainFixture(t, service, `{}`); result.Results[0].State != maintenanceOperator {
		t.Fatal("maintenance renewed unknown submission")
	}
}
