package main

import (
	"net/http"
	"testing"
)

func TestSessionAbsorbsARotationFromAnOrdinaryResponse(t *testing.T) {
	session := newWebSession(http.DefaultClient, webCredential{Cookie: "SAPISID=old; __Secure-1PSIDTS=stale", AuthUser: 0}, "https://gemini.google.com")
	response := &http.Response{Header: http.Header{"Set-Cookie": {
		"__Secure-1PSIDTS=fresh; Path=/; Secure; Domain=.google.com",
		"__Secure-1PSIDCC=carried; Path=/; Secure; Domain=.google.com",
	}}}

	session.absorb(response)

	if !session.rotated {
		t.Fatal("an ordinary rotation was dropped")
	}
	if !contains(session.cookie, "__Secure-1PSIDTS=fresh") || !contains(session.cookie, "__Secure-1PSIDCC=carried") {
		t.Fatalf("jar did not carry the rotation: %s", session.cookie)
	}
	if !contains(session.cookie, "SAPISID=old") {
		t.Fatalf("absorption dropped an untouched cookie: %s", session.cookie)
	}
}

func TestSessionIgnoresCookiesFromAnUnrelatedOrigin(t *testing.T) {
	session := newWebSession(http.DefaultClient, webCredential{Cookie: "SAPISID=old", AuthUser: 0}, "https://gemini.google.com")
	response := &http.Response{Header: http.Header{"Set-Cookie": {
		"__Secure-1PSIDTS=evil; Path=/; Secure; Domain=.attacker.example",
		"loose=value; Path=/some; Secure; Domain=.google.com",
	}}}

	session.absorb(response)

	if session.rotated || session.cookie != "SAPISID=old" {
		t.Fatalf("jar widened to an unrelated origin: %s", session.cookie)
	}
}

func TestPersistJarStoresTheRotatedCredential(t *testing.T) {
	service, local := continuationFixture(t)
	session := newWebSession(http.DefaultClient, webCredential{Cookie: "SAPISID=old", AuthUser: 0}, "https://gemini.google.com")
	session.cookie, session.rotated = "SAPISID=old; __Secure-1PSIDTS=fresh", true

	service.persistJar(local.Target.TokenRef, session)

	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := decodeWebCredential(sessionToken{stored.Token})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Cookie != session.cookie {
		t.Fatalf("rotation was not persisted: %s", credential.Cookie)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestJarIsStoredTheMomentGoogleRotatesMidCall(t *testing.T) {
	service, local := continuationFixture(t)
	session := newWebSession(http.DefaultClient, webCredential{Cookie: "SAPISID=old", AuthUser: 0}, "https://gemini.google.com")
	service.trackJar(local.Target.TokenRef, session)

	session.absorb(&http.Response{Header: http.Header{"Set-Cookie": {"__Secure-1PSIDTS=mid; Path=/; Secure; Domain=.google.com"}}})

	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := decodeWebCredential(sessionToken{stored.Token})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(credential.Cookie, "__Secure-1PSIDTS=mid") {
		t.Fatalf("a rotation during the call was not stored until it ended: %s", credential.Cookie)
	}
}

func TestSessionAbsorbsSidccWhichArrivesWithoutSecure(t *testing.T) {
	session := newWebSession(http.DefaultClient, webCredential{Cookie: "SAPISID=old; SIDCC=stale", AuthUser: 0}, "https://gemini.google.com")

	session.absorb(&http.Response{Header: http.Header{"Set-Cookie": {
		"SIDCC=fresh; Path=/; Domain=.google.com",
		"__Secure-1PSIDCC=fresh1p; Path=/; Secure; Domain=.google.com",
	}}})

	if !contains(session.cookie, "SIDCC=fresh") || !contains(session.cookie, "__Secure-1PSIDCC=fresh1p") {
		t.Fatalf("a half-rotated jar was stored: %s", session.cookie)
	}
}

func TestTrackedJarPreservesOtherSessionsRotations(t *testing.T) {
	for _, finishOld := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "completion"}[finishOld], func(t *testing.T) {
			service, local := continuationFixture(t)
			credential, err := decodeWebCredential(sessionToken{local.Token})
			if err != nil {
				t.Fatal(err)
			}
			old := service.newSession(credential)
			newer := service.newSession(credential)
			service.trackJar(local.Target.TokenRef, old)
			service.trackJar(local.Target.TokenRef, newer)
			old.absorb(&http.Response{Header: http.Header{"Set-Cookie": {
				"SIDCC=first; Path=/; Domain=.google.com",
			}}})
			newer.absorb(&http.Response{Header: http.Header{"Set-Cookie": {
				"__Secure-1PSIDTS=current; Path=/; Domain=.google.com",
				"SIDCC=current; Path=/; Domain=.google.com",
			}}})

			if finishOld {
				service.persistJar(local.Target.TokenRef, old)
			} else {
				old.absorb(&http.Response{Header: http.Header{"Set-Cookie": {
					"NID=updated; Path=/; Domain=.google.com",
				}}})
			}

			stored, err := service.sessions.read(local.Target.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeWebCredential(sessionToken{stored.Token})
			if err != nil {
				t.Fatal(err)
			}
			if !contains(got.Cookie, "__Secure-1PSIDTS=current") || !contains(got.Cookie, "SIDCC=current") {
				t.Fatal("older session overwrote the newer session's cookies")
			}
			if !finishOld && !contains(got.Cookie, "NID=updated") {
				t.Fatal("the response's actual update was lost")
			}
		})
	}
}
