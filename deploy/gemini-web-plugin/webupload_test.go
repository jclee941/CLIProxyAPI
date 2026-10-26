package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestUploadCarriesThePageStorageContextAndMediaType(t *testing.T) {
	for _, mimeType := range []string{"video/mp4", "image/png"} {
		t.Run(mimeType, func(t *testing.T) {
			// Given: the app publishes the storage target for this page.
			content := bytes.Repeat([]byte{0, 1, 2, 3}, 768*1024)
			var started, finalized bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/u/2/app" {
					writeFixture(t, w, `{"SNlM0e":"xsrf","cfb2h":"build","qKIAYe":"feeds/page-storage","Ylro7b":"page-context"}`)
					return
				}
				if r.Header.Get("Push-ID") != "feeds/page-storage" || r.Header.Get("X-Client-Pctx") != "page-context" {
					t.Error("upload did not carry the page's storage target and privacy context")
				}
				if r.Header.Get("X-Tenant-Id") != "bard-storage" || r.Header.Get("X-Goog-AuthUser") != "2" {
					t.Error("upload did not preserve the tenant and selected account")
				}
				switch r.Header.Get("X-Goog-Upload-Command") {
				case "start":
					started = true
					if r.Header.Get("X-Goog-Upload-Header-Content-Type") != mimeType ||
						r.Header.Get("X-Goog-Upload-Header-Content-Length") != strconv.Itoa(len(content)) {
						t.Error("upload start did not declare the media type and byte length")
					}
					w.Header().Set("X-Goog-Upload-Url", "http://"+r.Host+"/upload/finalize")
				case "upload, finalize":
					finalized = true
					body, err := io.ReadAll(r.Body)
					if err != nil || !bytes.Equal(body, content) || r.ContentLength != int64(len(content)) {
						t.Error("finalized upload did not preserve the complete media bytes")
					}
					if r.Header.Get("Content-Type") != mimeType || r.Header.Get("X-Goog-Upload-Offset") != "0" {
						t.Error("finalized upload did not carry the media type and offset")
					}
					writeFixture(t, w, "/contrib_service/ttl_1d/media")
				default:
					t.Error("unexpected upload command")
				}
			}))
			defer server.Close()
			session := newWebSession(server.Client(), webCredential{Cookie: "SID=test", AuthUser: 2}, server.URL)
			if err := session.bootstrap(context.Background()); err != nil {
				t.Fatal(err)
			}
			session.uploadOrigin = server.URL

			// When: the attachment is sent through both resumable upload legs.
			attachment, err := session.upload(context.Background(), "media"+webUploadKinds[mimeType].extension, mimeType, int64(len(content)), bytes.NewReader(content))

			// Then: the usable reference retains its type and both legs completed.
			if err != nil {
				t.Fatal(err)
			}
			if !started || !finalized || attachment.Path != "/contrib_service/ttl_1d/media" || attachment.MIMEType != mimeType {
				t.Fatal("upload did not return the completed media reference")
			}
		})
	}
}

// The web product takes a known set of files. Anything else has to be refused
// here, because upstream answers an unsupported upload with a reference error
// that names neither the file nor the reason.
func TestUploadRefusesATypeTheWebProductCannotTake(t *testing.T) {
	session := &webSession{}

	source, err := inlineSource(webMedia{MIMEType: "application/x-msdownload", Data: "AAAA"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := session.uploadSources(context.Background(), []webSource{source}); err == nil {
		t.Fatal("an unsupported attachment type was accepted")
	}
}

func TestAttachmentNoticeNamesWhatWasAttached(t *testing.T) {
	for mimeType, want := range map[string]string{
		"image/png":                 "[Image attached]",
		"application/pdf":           "[Document attached]",
		"text/plain; charset=utf-8": "[Document attached]",
	} {
		if notice := webAttachmentNotice(mimeType); notice != want {
			t.Fatalf("%s was announced as %q, want %q", mimeType, notice, want)
		}
	}
}

func TestAttachmentSlotMatchesTheCapturedWire(t *testing.T) {
	slot := webAttachmentSlot([]webAttachment{{Path: "/contrib_service/ttl_1d/abc", Name: "attach.png", MIMEType: "image/png", ClientID: "fixed-id"}})

	encoded, err := json.Marshal(slot)
	if err != nil {
		t.Fatal(err)
	}
	want := `[[["/contrib_service/ttl_1d/abc",1,null,"image/png","fixed-id"],"attach.png",null,null,null,null,null,null,[0]]]`
	if string(encoded) != want {
		t.Fatalf("attachment slot drifted from the captured wire:\n got %s\nwant %s", encoded, want)
	}
}

func TestAttachmentSlotIsAbsentWithoutFiles(t *testing.T) {
	if slot := webAttachmentSlot(nil); slot != nil {
		t.Fatalf("an empty attachment list still occupied the slot: %v", slot)
	}
}

func TestGenerationFieldsCarryAttachmentsBesideThePrompt(t *testing.T) {
	fields := webGenerationFields("hello", 1, 0, "conversation", []webAttachment{{Path: "/contrib_service/ttl_1d/abc", Name: "a.png", MIMEType: "image/png", ClientID: "id"}})

	prompt, ok := fields[0].([]any)
	if !ok || len(prompt) != 7 {
		t.Fatalf("prompt slot shape changed: %#v", fields[0])
	}
	if prompt[3] == nil {
		t.Fatal("attachments were not placed in slot 3")
	}
}
