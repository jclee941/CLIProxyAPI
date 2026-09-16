package main

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
	return session
}
