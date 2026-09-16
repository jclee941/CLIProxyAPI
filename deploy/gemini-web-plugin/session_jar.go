package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// persistJar stores a jar that Google rotated during ordinary traffic. Without
// it the plugin keeps sending a cookie upstream has already retired, which is
// what made healthy accounts expire at random under load.
func (service *service) trackJar(reference string, session *webSession) {
	session.onRotate = func(cookie string) { service.storeJar(reference, cookie) }
}

func (service *service) persistJar(reference string, session *webSession) {
	if session == nil || !session.rotated {
		return
	}
	service.storeJar(reference, session.cookie)
}

func (service *service) storeJar(reference string, cookie string) {
	store := service.localStore()
	if store == nil || !localReferencePattern.MatchString(reference) {
		return
	}
	local, err := store.read(reference)
	if err != nil {
		return
	}
	credential, err := decodeWebCredential(sessionToken{local.Token})
	if err != nil || credential.Cookie == cookie {
		return
	}
	refreshed := encodeWebCredential(cookie, credential.AuthUser)
	if refreshed.value == "" {
		return
	}
	local.Token = refreshed.value
	if err := store.write(local); err != nil {
		return
	}
}

// newSession builds a web session with the overrides tests use to keep every
// upstream call, including the upload host, inside the fixture.
func (service *service) newSession(credential webCredential) *webSession {
	session := newWebSession(service.client, credential, service.webOriginOverride)
	if service.webUploadOverride != "" {
		session.uploadOrigin = service.webUploadOverride
	}
	session.onCut = service.reportCut
	return session
}

// reportUnnamed records a submission that finished without an operation to
// recover. Which slots the stream actually carried is the whole difference
// between fixing where the receipt is read and guessing at it for another ten
// minutes, so the layout goes out with it; the content never does.
func (service *service) reportUnnamed(code string, turn continuationTurn, lines int, shapes []string) {
	// No frame at all is the loudest answer this can give, not a reason to say
	// nothing: it separates a stream that carried no payload from one whose
	// payload moved. Counting the lines alongside separates both of those from a
	// stream that carried nothing whatsoever.
	layout := strings.Join(shapes, " | ")
	if layout == "" {
		layout = "no payload frame"
	}
	if len(layout) > 400 {
		layout = layout[:400]
	}
	service.report(map[string]any{
		"provider": provider,
		"state":    "submission_unnamed",
		"error":    code,
		"budget": fmt.Sprintf("%d frames from %d lines, conversation=%t reply=%t candidate=%t",
			len(shapes), lines, turn.Conversation != "", turn.Reply != "", turn.Candidate != ""),
		"reason": layout,
	}, "gemini-web: submission named no operation")
}

func (service *service) reportCut(fields map[string]any) {
	service.report(fields, "gemini-web: generation stream cut")
}

// report sends one line through the host, which is where an operator reads and
// where the request id is attached. The level is a warning because lower ones
// are filtered out of the deployed log, and a report written nowhere leaves the
// same silence it is meant to end. A log that cannot be delivered is not worth
// failing the request it describes.
func (service *service) report(fields map[string]any, message string) {
	if service.host == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{"level": "warn", "message": message, "fields": fields})
	if err != nil {
		return
	}
	_, _ = service.host("host.log", payload)
}
