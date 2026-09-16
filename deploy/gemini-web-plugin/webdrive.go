package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Drive is reachable with an API key, but a key authorises the caller rather
// than a user: it reads a file shared as "anyone with the link" and nothing
// else. A private file answers 404 and listing answers 403, so there is no
// browsing here and no way to widen the reach - a caller names one file and the
// bytes join the ordinary attachment path.

const driveOrigin = "https://www.googleapis.com"

var driveFilePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)

// driveFileID recognises the forms a caller actually has to hand: the share
// link, the open and uc query links, and a bare drive: reference.
func driveFileID(reference string) (string, bool) {
	trimmed := strings.TrimSpace(reference)
	if identifier := strings.TrimPrefix(trimmed, "drive:"); identifier != trimmed {
		return identifier, driveFilePattern.MatchString(identifier)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host != "drive.google.com" && parsed.Host != "docs.google.com" {
		return "", false
	}
	if identifier := parsed.Query().Get("id"); identifier != "" {
		return identifier, driveFilePattern.MatchString(identifier)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index, segment := range segments {
		if segment == "d" && index+1 < len(segments) {
			return segments[index+1], driveFilePattern.MatchString(segments[index+1])
		}
	}
	return "", false
}

// resolveMedia turns every Drive reference into bytes and leaves inline media
// untouched, so the upload path sees one kind of attachment.
func (service *service) resolveMedia(ctx context.Context, media []webMedia) ([]webMedia, error) {
	for index, item := range media {
		if item.Reference == "" {
			continue
		}
		fetched, err := service.fetchDrive(ctx, item.Reference)
		if err != nil {
			return nil, err
		}
		media[index] = fetched
	}
	return media, nil
}

// fetchDrive reads one shared file. The metadata call names the media type the
// attachment path needs, which Drive knows and the caller would otherwise have
// to repeat, and the media call carries the bytes under the same bound an
// inline attachment gets.
func (service *service) fetchDrive(ctx context.Context, reference string) (webMedia, error) {
	identifier, ok := driveFileID(reference)
	if !ok {
		return webMedia{}, failure(400, "attachment_reference_unsupported")
	}
	bearer, err := service.driveAuthorization(ctx)
	if err != nil {
		return webMedia{}, err
	}
	key := service.driveSecret(service.settings().DriveAPIKey, "GOOGLE_DRIVE_API_KEY")
	if bearer == "" && key == "" {
		return webMedia{}, failure(400, "drive_credentials_missing")
	}
	origin := driveOrigin
	if service.driveOverride != "" {
		origin = service.driveOverride
	}
	endpoint := origin + "/drive/v3/files/" + url.PathEscape(identifier)
	credential := ""
	if bearer == "" {
		credential = "&key=" + url.QueryEscape(key)
	}
	var metadata struct {
		MIMEType string `json:"mimeType"`
	}
	if err := service.driveMetadata(ctx, endpoint+"?fields=mimeType"+credential, bearer, &metadata); err != nil {
		return webMedia{}, err
	}
	if metadata.MIMEType == "" {
		return webMedia{}, failure(502, "drive_metadata_invalid")
	}
	content, err := service.driveDownload(ctx, endpoint+"?alt=media"+credential, bearer)
	if err != nil {
		return webMedia{}, err
	}
	return webMedia{MIMEType: metadata.MIMEType, Data: base64.StdEncoding.EncodeToString(content)}, nil
}

const driveTokenEndpoint = "https://oauth2.googleapis.com/token"

// driveToken holds one access token for its lifetime. Google issues a fresh
// token per refresh and rate limits that exchange, so concurrent attachments
// share the token rather than each buying their own.
type driveToken struct {
	mu      sync.Mutex
	value   string
	expires time.Time
}

// driveAuthorization returns a bearer token when an OAuth client is configured,
// and an empty string when it is not, which leaves the caller on the API key.
// The key only reaches files shared with anyone holding the link; OAuth is what
// reaches the operator's own private files.
// driveSecret prefers the plugin configuration, which is mounted and hot
// reloaded, over the process environment, which changes only when the container
// is recreated.
func (service *service) driveSecret(configured, variable string) string {
	if configured != "" {
		return configured
	}
	return os.Getenv(variable)
}

func (service *service) driveAuthorization(ctx context.Context) (string, error) {
	settings := service.settings()
	identifier := service.driveSecret(settings.DriveClientID, "GOOGLE_DRIVE_CLIENT_ID")
	secret := service.driveSecret(settings.DriveClientSecret, "GOOGLE_DRIVE_CLIENT_SECRET")
	refresh := service.driveSecret(settings.DriveRefreshToken, "GOOGLE_DRIVE_REFRESH_TOKEN")
	if identifier == "" || secret == "" || refresh == "" {
		return "", nil
	}
	service.driveAccess.mu.Lock()
	defer service.driveAccess.mu.Unlock()
	if service.driveAccess.value != "" && service.now().Before(service.driveAccess.expires) {
		return service.driveAccess.value, nil
	}
	endpoint := driveTokenEndpoint
	if service.driveTokenOverride != "" {
		endpoint = service.driveTokenOverride
	}
	form := url.Values{
		"client_id":     {identifier},
		"client_secret": {secret},
		"refresh_token": {refresh},
		"grant_type":    {"refresh_token"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", failure(400, "drive_request_invalid")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := service.client.Do(request)
	if err != nil {
		return "", failure(502, "drive_unreachable")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if closeErr := response.Body.Close(); closeErr != nil {
		_ = closeErr
	}
	if readErr != nil || response.StatusCode != http.StatusOK {
		return "", failure(502, "drive_authorization_failed")
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if json.Unmarshal(body, &token) != nil || token.AccessToken == "" {
		return "", failure(502, "drive_authorization_failed")
	}
	lifetime := time.Duration(token.ExpiresIn) * time.Second
	// Retire the token early so one that expires mid-download is not the first
	// thing the attachment path discovers.
	if lifetime > time.Minute {
		lifetime -= time.Minute
	}
	service.driveAccess.value, service.driveAccess.expires = token.AccessToken, service.now().Add(lifetime)
	return token.AccessToken, nil
}

func (service *service) driveMetadata(ctx context.Context, endpoint, bearer string, target any) error {
	body, err := service.driveGet(ctx, endpoint, bearer, 64*1024)
	if err != nil {
		return err
	}
	if json.Unmarshal(body, target) != nil {
		return failure(502, "drive_metadata_invalid")
	}
	return nil
}

func (service *service) driveDownload(ctx context.Context, endpoint, bearer string) ([]byte, error) {
	// One byte past the attachment bound, so an oversized file is refused rather
	// than silently truncated into a corrupt upload.
	content, err := service.driveGet(ctx, endpoint, bearer, webUploadLimit+1)
	if err != nil {
		return nil, err
	}
	if len(content) > webUploadLimit {
		return nil, failure(400, "attachment_size_rejected")
	}
	return content, nil
}

func (service *service) driveGet(ctx context.Context, endpoint, bearer string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, failure(400, "drive_request_invalid")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := service.client.Do(request)
	if err != nil {
		return nil, failure(502, "drive_unreachable")
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, limit))
	if closeErr := response.Body.Close(); closeErr != nil {
		_ = closeErr
	}
	if readErr != nil {
		return nil, failure(502, "drive_unreachable")
	}
	// A key cannot see a private file, and Drive reports that as absence rather
	// than denial, so this is the answer an unshared file gives.
	if response.StatusCode == http.StatusNotFound {
		return nil, failure(404, "drive_file_not_shared")
	}
	if response.StatusCode != http.StatusOK {
		return nil, failure(502, "drive_rejected")
	}
	return content, nil
}
