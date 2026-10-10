package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
)

const flowUploadAAD = "flow2api-resumable-upload-v1"

type flowUploadClaims struct {
	AccountID   string `json:"account"`
	ProjectID   string `json:"project"`
	URL         string `json:"url"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Granularity int64  `json:"granularity"`
}

func flowUploadCipher() (cipher.AEAD, error) {
	secret := brokerCredential()
	if secret == "" {
		return nil, failure(503, "flow_session_broker_unconfigured")
	}
	key := sha256.Sum256([]byte(flowUploadAAD + "\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealFlowUpload(claims flowUploadClaims) (string, error) {
	aead, err := flowUploadCipher()
	if err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, []byte(flowUploadAAD))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (service *service) openFlowUpload(token, account string) (flowUploadClaims, error) {
	var claims flowUploadClaims
	if token == "" || len(token) > 8192 {
		return claims, failure(404, "flow_upload_not_found")
	}
	aead, err := flowUploadCipher()
	if err != nil {
		return claims, err
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	n := aead.NonceSize()
	if err != nil || len(raw) < n+aead.Overhead() || base64.RawURLEncoding.EncodeToString(raw) != token {
		return claims, failure(404, "flow_upload_not_found")
	}
	plaintext, err := aead.Open(nil, raw[:n], raw[n:], []byte(flowUploadAAD))
	if err != nil {
		return claims, failure(404, "flow_upload_not_found")
	}
	defer clear(plaintext)
	if json.Unmarshal(plaintext, &claims) != nil || claims.AccountID != account ||
		!flowUUIDPattern.MatchString(claims.ProjectID) || claims.Size < 1 || claims.Size > 1<<30 ||
		claims.Granularity < 1 || claims.Granularity > flowUploadChunkBytes {
		return flowUploadClaims{}, failure(404, "flow_upload_not_found")
	}
	origin := flowOrigin
	if service.flowOriginOverride != "" {
		origin = service.flowOriginOverride
	}
	if !flowUploadURL(claims.URL, origin, claims.ProjectID) {
		return flowUploadClaims{}, failure(404, "flow_upload_not_found")
	}
	return claims, nil
}

func flowUploadURL(raw, origin, project string) bool {
	target, err := url.Parse(raw)
	base, errBase := url.Parse(origin)
	return err == nil && errBase == nil && target.Scheme == "https" && target.Host == base.Host &&
		target.User == nil && target.Fragment == "" && target.RawPath == "" &&
		target.Path == "/upload/v1/flow/upload/video/"+project
}
