package main

import "encoding/json"

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

// reportCut records a cut generation stream through the host, which is where an
// operator reads and where the request id is attached. The level is a warning
// because lower ones are filtered out of the deployed log, and a cut written
// nowhere leaves the same silence it is meant to end. A log that cannot be
// delivered is not worth failing the request it describes.
func (service *service) reportCut(fields map[string]any) {
	if service.host == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"level":   "warn",
		"message": "gemini-web: generation stream cut",
		"fields":  fields,
	})
	if err != nil {
		return
	}
	_, _ = service.host("host.log", payload)
}
