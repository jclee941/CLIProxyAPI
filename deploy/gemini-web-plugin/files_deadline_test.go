package main

import (
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

type filesDeadlineProbe struct {
	t    *testing.T
	base http.RoundTripper
}

func (probe filesDeadlineProbe) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/token" {
		if _, bounded := request.Context().Deadline(); bounded {
			probe.t.Errorf("unexpected post-acquisition deadline on %s", request.URL.Path)
		}
	}
	return probe.base.RoundTrip(request)
}

func TestFilesDriveTransfersDoNotAddNetworkDeadlines(t *testing.T) {
	service := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	newFilesDriveFixture(t).attach(service)
	service.client.Transport = filesDeadlineProbe{t: t, base: http.DefaultTransport}
	file, err := service.saveGeneratedFile(t.Context(), filesTestCaller, "deadline-probe", "video/mp4", []byte("deadline-free Drive stream"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.resolveFileSource(filesTestCaller, file.URI)
	if err != nil {
		t.Fatal(err)
	}
	body, err := source.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, body); err != nil {
		t.Error(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
}
