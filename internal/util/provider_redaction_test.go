package util

import (
	"net/url"
	"strings"
	"testing"
)

func TestMaskUploadBearerCredentials(t *testing.T) {
	for _, secret := range []string{"x", "ab", "session-opaque-credentials", "bad%escape"} {
		for _, key := range []string{"upload_id", "UPLOAD_ID", "upload_id[]", "upload%5fid"} {
			raw := "q=one+two&" + key + "=" + url.QueryEscape(secret) + "&" + key + "=" + url.QueryEscape(secret)
			masked := MaskSensitiveQuery(raw)
			values, err := url.ParseQuery(masked)
			if err != nil {
				t.Fatal(err)
			}
			decodedKey, err := url.QueryUnescape(key)
			if err != nil {
				t.Fatal(err)
			}
			if got := values[decodedKey]; len(got) != 2 || got[0] != "[REDACTED]" || got[1] != "[REDACTED]" {
				t.Errorf("upload bearer value not fully masked: %q", masked)
			}
			if !strings.HasPrefix(masked, "q=one+two&") {
				t.Errorf("unrelated query changed: %q", masked)
			}
		}
		for _, key := range []string{"X-Goog-Upload-URL", "x-goog-upload-url", " X-GOOG-UPLOAD-URL "} {
			if got := MaskSensitiveHeaderValue(key, secret); got != "[REDACTED]" {
				t.Errorf("upload URL header not fully masked: %q", got)
			}
		}
	}
	if got := MaskSensitiveQuery("upload_id=bad%escape&upload_id&alt=json"); got != "upload_id=%5BREDACTED%5D&upload_id=%5BREDACTED%5D&alt=json" {
		t.Errorf("malformed or valueless upload token not masked: %q", got)
	}
	if got := MaskSensitiveHeaderValue("X-Goog-Upload-Offset", "128"); got != "128" {
		t.Errorf("non-secret upload header changed: %q", got)
	}
	if got := MaskSensitiveQuery("key=1234567890&upload_type=resumable"); got != "key=1234...7890&upload_type=resumable" {
		t.Errorf("existing API key or non-secret query masking changed: %q", got)
	}
}
