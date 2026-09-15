package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localRecordFixture(t *testing.T) localSession {
	t.Helper()
	record := recordFixture(t, "a")
	record.TokenRef = "session://gemini-web/" + strings.Repeat("a", 32)
	record.SessionRevision = 1
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	return localSession{Target: record, Projection: string(auth.StorageJSON), Token: encodedToken("test-local"), Identity: credentialInspection{AccountSHA256: testAccountDigest, AuthUser: 2}, State: localHostPending}
}

func sessionKeyFixture() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

func TestLocalStorePersistsEncryptedRecord_whenReopened(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	store, err := openSessionStore(directory, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	record := localRecordFixture(t)
	if err := store.write(record); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openSessionStore(directory, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.close(); err != nil {
			t.Error(err)
		}
	})
	got, err := reopened.read(record.Target.TokenRef)

	if err != nil || got != record {
		t.Fatalf("roundtrip failed: %v", err)
	}
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range files {
		raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(record.Token)) || bytes.Contains(raw, []byte("test-local")) {
			t.Fatal("plaintext persisted")
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe file mode")
		}
	}
}

func TestLocalStoreRejectsSecondWriterAndWrongKey_whenRecordExists(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	store, err := openSessionStore(directory, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.write(localRecordFixture(t)); err != nil {
		t.Fatal(err)
	}
	if other, err := openSessionStore(directory, sessionKeyFixture()); err == nil {
		t.Error("second writer admitted")
		if err := other.close(); err != nil {
			t.Error(err)
		}
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	other, err := openSessionStore(directory, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))

	if err == nil {
		t.Error("wrong key admitted")
		if err := other.close(); err != nil {
			t.Error(err)
		}
	}
}
