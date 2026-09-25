package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
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

// mediaSources describes every attachment to the upload path without moving any
// bytes yet. A Drive reference contributes its type and length from metadata, so
// the file itself only ever travels once, straight into the upload.
func (service *service) mediaSources(ctx context.Context, media []webMedia, caller string) ([]webSource, error) {
	sources := make([]webSource, 0, len(media))
	for _, item := range media {
		var source webSource
		var err error
		if _, stored := filesReferenceID(item.Reference); stored {
			source, err = service.resolveFileSource(caller, item.Reference)
		} else {
			source, err = service.mediaSource(ctx, item)
		}
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func (service *service) mediaSource(ctx context.Context, item webMedia) (webSource, error) {
	if item.Reference == "" {
		return inlineSource(item)
	}
	return service.driveSource(ctx, item.Reference)
}

// fetchDrive reads one shared file. The metadata call names the media type the
// attachment path needs, which Drive knows and the caller would otherwise have
// to repeat, and the media call carries the bytes under the same bound an
// inline attachment gets.
func (service *service) driveSource(ctx context.Context, reference string) (webSource, error) {
	identifier, ok := driveFileID(reference)
	if !ok {
		return webSource{}, failure(400, "attachment_reference_unsupported")
	}
	bearer, err := service.driveAuthorization(ctx)
	if err != nil {
		return webSource{}, err
	}
	key := service.driveSecret(service.settings().DriveAPIKey, "GOOGLE_DRIVE_API_KEY")
	if bearer == "" && key == "" {
		return webSource{}, failure(400, "drive_credentials_missing")
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
		Size     string `json:"size"`
	}
	if err := service.driveMetadata(ctx, endpoint+"?fields=mimeType,size"+credential, bearer, &metadata); err != nil {
		return webSource{}, err
	}
	size, err := strconv.ParseInt(metadata.Size, 10, 64)
	if metadata.MIMEType == "" || err != nil || size <= 0 {
		// A Google Doc and a folder have no byte length, and neither is a file
		// this can hand to an upload that must declare one.
		return webSource{}, failure(400, "drive_file_not_downloadable")
	}
	media := endpoint + "?alt=media" + credential
	return webSource{MIMEType: metadata.MIMEType, Size: size, Open: func(ctx context.Context) (io.ReadCloser, error) {
		return service.driveStream(ctx, media, bearer)
	}}, nil
}

// driveStream opens the file without reading it, so the bytes go straight into
// the upload instead of being held here in full and again as base64.
func (service *service) driveStream(ctx context.Context, endpoint, bearer string) (io.ReadCloser, error) {
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
	if response.StatusCode != http.StatusOK {
		if drainErr := drainResponse(response); drainErr != nil {
			_ = drainErr
		}
		if response.StatusCode == http.StatusNotFound {
			return nil, failure(404, "drive_file_not_shared")
		}
		return nil, failure(502, "drive_rejected")
	}
	return response.Body, nil
}

const driveTokenEndpoint = "https://oauth2.googleapis.com/token"

// driveToken holds one access token for its lifetime. Google issues a fresh
// token per refresh and rate limits that exchange, so concurrent attachments
// share the token rather than each buying their own.
type driveToken struct {
	mu      sync.Mutex
	value   string
	scopes  string
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
		Scope       string `json:"scope"`
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
	service.driveAccess.scopes = token.Scope
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
