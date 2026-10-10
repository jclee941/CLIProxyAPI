package main

import (
	"context"
	"testing"
	"time"
)

func TestFlowSessionDiscardsRejectedBootstrapWithoutRepeatingCall(t *testing.T) {
	for _, code := range []string{"flow_unauthenticated", "flow_upstream_status"} {
		t.Run(code, func(t *testing.T) {
			// Given page tokens captured before the account credential changed.
			service := newService(nil)
			record := storageRecord{SourceAuthID: "gemini-web-f.json"}
			service.flowPages = map[string]flowPage{
				record.SourceAuthID: {build: "old-build", xsrf: "old-token", read: time.Unix(1000, 0)},
			}
			calls := 0
			// When the upstream refuses that session.
			err := service.withFlowSession(context.Background(), record, func(session *flowSession) error {
				calls++
				if session.page.build != "old-build" {
					t.Fatal("fixture did not use the cached bootstrap")
				}
				return failure(502, code)
			})
			// Then the failure is unchanged and the next call must bootstrap again.
			if safeCredentialCode(err) != code || calls != 1 || service.newFlowSession(record.SourceAuthID).page.build != "" {
				t.Fatalf("code=%s calls=%d page=%+v", safeCredentialCode(err), calls, service.flowPages)
			}
		})
	}
}
