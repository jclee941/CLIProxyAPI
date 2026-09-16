package main

// persistJar stores a jar that Google rotated during ordinary traffic. Without
// it the plugin keeps sending a cookie upstream has already retired, which is
// what made healthy accounts expire at random under load.
func (service *service) persistJar(record storageRecord, session *webSession) {
	if session == nil || !session.rotated {
		return
	}
	store := service.localStore()
	if store == nil || !localReferencePattern.MatchString(record.TokenRef) {
		return
	}
	local, err := store.read(record.TokenRef)
	if err != nil {
		return
	}
	credential, err := decodeWebCredential(sessionToken{local.Token})
	if err != nil || credential.Cookie == session.cookie {
		return
	}
	refreshed := encodeWebCredential(session.cookie, credential.AuthUser)
	if refreshed.value == "" {
		return
	}
	local.Token = refreshed.value
	if err := store.write(local); err != nil {
		return
	}
}
