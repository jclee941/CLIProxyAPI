package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"
)

const (
	webImageModel    = "gpt-web-image"
	webBase          = "https://chatgpt.com"
	webClientVersion = "prod-a194cd50d4416d3c0b47c740f206b12ce60f5887"
	webClientBuild   = "6708908"
	webBrowserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
	webUpstreamModel = "auto"

	webInitialWait  = 10 * time.Second
	webPollInterval = 5 * time.Second
	// Measured generations land in 19-40s. The budget is walked once per
	// credential, so a longer one only delays a typed error past the point where
	// callers give up and drop the connection, turning a reportable failure into
	// a disconnect.
	webPollBudget = 120 * time.Second
)

var (
	webScriptSourceRE = regexp.MustCompile(`<script[^>]+src="([^"]+)"`)
	webDataBuildRE    = regexp.MustCompile(`c/[^/]*/_`)
	webHTMLBuildRE    = regexp.MustCompile(`<html[^>]*data-build="([^"]*)"`)
	webConversationRE = regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`)
	webFileServiceRE  = regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)`)
	webImageFileRE    = regexp.MustCompile(`\bfile_00000000[a-f0-9]{24}\b`)
	webSedimentRE     = regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`)
)

type webRequirements struct {
	Token      string
	ProofToken string
}

type webClient struct {
	http          *http.Client
	token         string
	deviceID      string
	sessionID     string
	scriptSources []string
	dataBuild     string
}

func webImageModels() []modelInfo {
	return []modelInfo{{
		ID:                        webImageModel,
		Object:                    "model",
		OwnedBy:                   provider,
		Type:                      imagesModelType,
		DisplayName:               "ChatGPT Web Image",
		Name:                      webImageModel,
		Description:               "Image generation through the ChatGPT web conversation session; spends the web image_gen allowance instead of the Codex API allowance",
		SupportedInputModalities:  []string{"text"},
		SupportedOutputModalities: []string{"image"},
	}}
}

func claimsWebImageModel(model string) bool {
	return imagesModelBase(model) == webImageModel
}

func newWebClient(token string) (*webClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, failure(500, "web_cookie_jar_failed")
	}
	return &webClient{
		http:      &http.Client{Jar: jar, Transport: &http.Transport{ForceAttemptHTTP2: true, MaxIdleConnsPerHost: 4}},
		token:     token,
		deviceID:  newDeviceID(),
		sessionID: newDeviceID(),
	}, nil
}

func (client *webClient) header(path string, extra map[string]string) http.Header {
	header := http.Header{}
	header.Set("User-Agent", webBrowserAgent)
	header.Set("Origin", webBase)
	header.Set("Referer", webBase+"/")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	header.Set("Cache-Control", "no-cache")
	header.Set("Pragma", "no-cache")
	header.Set("Priority", "u=1, i")
	header.Set("Sec-Ch-Ua", `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`)
	header.Set("Sec-Ch-Ua-Arch", `"x86"`)
	header.Set("Sec-Ch-Ua-Bitness", `"64"`)
	header.Set("Sec-Ch-Ua-Full-Version", `"143.0.3650.96"`)
	header.Set("Sec-Ch-Ua-Full-Version-List", `"Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"`)
	header.Set("Sec-Ch-Ua-Mobile", "?0")
	header.Set("Sec-Ch-Ua-Model", `""`)
	header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	header.Set("Sec-Ch-Ua-Platform-Version", `"19.0.0"`)
	header.Set("Sec-Fetch-Dest", "empty")
	header.Set("Sec-Fetch-Mode", "cors")
	header.Set("Sec-Fetch-Site", "same-origin")
	header.Set("OAI-Device-Id", client.deviceID)
	header.Set("OAI-Session-Id", client.sessionID)
	header.Set("OAI-Language", "en-US")
	header.Set("OAI-Client-Version", webClientVersion)
	header.Set("OAI-Client-Build-Number", webClientBuild)
	header.Set("Authorization", "Bearer "+client.token)
	header.Set("X-OpenAI-Target-Path", path)
	header.Set("X-OpenAI-Target-Route", path)
	for key, value := range extra {
		header.Set(key, value)
	}
	return header
}

func (client *webClient) imageHeader(path string, requirements webRequirements, conduitToken, accept string) http.Header {
	extra := map[string]string{"Content-Type": "application/json", "Accept": accept}
	header := client.header(path, extra)
	header.Set("OpenAI-Sentinel-Chat-Requirements-Token", requirements.Token)
	if requirements.ProofToken != "" {
		header.Set("OpenAI-Sentinel-Proof-Token", requirements.ProofToken)
	}
	if conduitToken != "" {
		header.Set("X-Conduit-Token", conduitToken)
	}
	if accept == "text/event-stream" {
		header.Set("X-Oai-Turn-Trace-Id", newDeviceID())
	}
	return header
}

func (client *webClient) send(ctx context.Context, method, path string, header http.Header, body []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		request, err := http.NewRequestWithContext(ctx, method, webBase+path, reader)
		if err != nil {
			return nil, failure(500, "web_request_invalid")
		}
		request.Header = header
		response, doErr := client.http.Do(request)
		if doErr == nil {
			return response, nil
		}
		if attempt+1 >= webSendAttempts || !webUnsentFailure(doErr) {
			return nil, failure(502, "web_transport_failed")
		}
		select {
		case <-ctx.Done():
			return nil, failure(499, "web_client_disconnected")
		case <-time.After(webRetryDelay(attempt)):
		}
	}
}

func closeBody(response *http.Response) {
	if closeErr := response.Body.Close(); closeErr != nil {
		_ = closeErr
	}
}

func (client *webClient) call(ctx context.Context, method, path string, header http.Header, body []byte, code string) ([]byte, error) {
	response, err := client.send(ctx, method, path, header, body)
	if err != nil {
		return nil, err
	}
	defer closeBody(response)
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if readErr != nil {
		return nil, failure(502, code)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, failure(response.StatusCode, code)
	}
	return payload, nil
}

func (client *webClient) bootstrap(ctx context.Context) error {
	header := client.header("/", map[string]string{
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	})
	header.Del("Authorization")
	header.Del("X-OpenAI-Target-Path")
	header.Del("X-OpenAI-Target-Route")
	body, err := client.call(ctx, http.MethodGet, "/", header, nil, "web_bootstrap_failed")
	if err != nil {
		return err
	}
	client.scriptSources, client.dataBuild = webParseResources(string(body))
	return nil
}

func webParseResources(html string) ([]string, string) {
	sources := []string{}
	dataBuild := ""
	for _, match := range webScriptSourceRE.FindAllStringSubmatch(html, -1) {
		sources = append(sources, match[1])
		if dataBuild == "" {
			if found := webDataBuildRE.FindString(match[1]); found != "" {
				dataBuild = found
			}
		}
	}
	if dataBuild == "" {
		if match := webHTMLBuildRE.FindStringSubmatch(html); len(match) == 2 {
			dataBuild = match[1]
		}
	}
	if len(sources) == 0 {
		sources = []string{powDefaultScript}
	}
	return sources, dataBuild
}

func (client *webClient) chatRequirements(ctx context.Context) (webRequirements, error) {
	legacy, err := powLegacyToken(webBrowserAgent, client.scriptSources, client.dataBuild)
	if err != nil {
		return webRequirements{}, err
	}
	preparePath := "/backend-api/sentinel/chat-requirements/prepare"
	request, err := json.Marshal(map[string]interface{}{"p": legacy})
	if err != nil {
		return webRequirements{}, failure(500, "web_request_encoding_failed")
	}
	raw, err := client.call(ctx, http.MethodPost, preparePath,
		client.header(preparePath, map[string]string{"Content-Type": "application/json"}), request, "web_sentinel_prepare_failed")
	if err != nil {
		return webRequirements{}, err
	}
	var prepared struct {
		PrepareToken string `json:"prepare_token"`
		Arkose       struct {
			Required bool `json:"required"`
		} `json:"arkose"`
		ProofOfWork struct {
			Required   bool   `json:"required"`
			Seed       string `json:"seed"`
			Difficulty string `json:"difficulty"`
		} `json:"proofofwork"`
		Turnstile struct {
			Required bool   `json:"required"`
			DX       string `json:"dx"`
		} `json:"turnstile"`
	}
	if json.Unmarshal(raw, &prepared) != nil {
		return webRequirements{}, failure(502, "web_sentinel_prepare_invalid")
	}
	if prepared.Arkose.Required {
		return webRequirements{}, failure(503, "web_arkose_required")
	}
	proofToken := ""
	if prepared.ProofOfWork.Required {
		proofToken, err = powProofToken(prepared.ProofOfWork.Seed, prepared.ProofOfWork.Difficulty,
			webBrowserAgent, client.scriptSources, client.dataBuild)
		if err != nil {
			return webRequirements{}, err
		}
	}

	finalizePath := "/backend-api/sentinel/chat-requirements/finalize"
	request, err = json.Marshal(map[string]interface{}{
		"prepare_token":   prepared.PrepareToken,
		"proof_token":     proofToken,
		"turnstile_token": "",
	})
	if err != nil {
		return webRequirements{}, failure(500, "web_request_encoding_failed")
	}
	raw, err = client.call(ctx, http.MethodPost, finalizePath,
		client.header(finalizePath, map[string]string{"Content-Type": "application/json"}), request, "web_sentinel_finalize_failed")
	if err != nil {
		return webRequirements{}, err
	}
	var finalized struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &finalized) != nil || finalized.Token == "" {
		return webRequirements{}, failure(502, "web_requirements_token_missing")
	}
	return webRequirements{Token: finalized.Token, ProofToken: proofToken}, nil
}

func (client *webClient) prepareConversation(ctx context.Context, prompt string, requirements webRequirements, model string, hints []string) (string, error) {
	path := "/backend-api/f/conversation/prepare"
	body := map[string]interface{}{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     newDeviceID(),
		"model":                 model,
		"client_prepare_state":  "success",
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]interface{}{"kind": "primary_assistant"},
		"partial_query": map[string]interface{}{
			"id":      newDeviceID(),
			"author":  map[string]interface{}{"role": "user"},
			"content": map[string]interface{}{"content_type": "text", "parts": []string{prompt}},
		},
		"supports_buffering":     true,
		"supported_encodings":    []string{"v1"},
		"client_contextual_info": map[string]interface{}{"app_name": "chatgpt.com"},
	}
	if len(hints) > 0 {
		body["system_hints"] = hints
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", failure(500, "web_request_encoding_failed")
	}
	raw, err := client.call(ctx, http.MethodPost, path,
		client.imageHeader(path, requirements, "", "*/*"), payload, "web_conversation_prepare_failed")
	if err != nil {
		return "", err
	}
	var prepared struct {
		ConduitToken string `json:"conduit_token"`
	}
	if json.Unmarshal(raw, &prepared) != nil {
		return "", failure(502, "web_conversation_prepare_invalid")
	}
	return prepared.ConduitToken, nil
}

func (client *webClient) startGeneration(ctx context.Context, prompt string, requirements webRequirements, conduitToken, model string, hints []string) (*http.Response, error) {
	path := "/backend-api/f/conversation"
	metadata := map[string]interface{}{
		"developer_mode_connector_ids": []string{},
		"selected_github_repos":        []string{},
		"selected_all_github_repos":    false,
		"serialization_metadata":       map[string]interface{}{"custom_symbol_offsets": []interface{}{}},
	}
	if len(hints) > 0 {
		metadata["system_hints"] = hints
	}
	body := map[string]interface{}{
		"action": "next",
		"messages": []interface{}{map[string]interface{}{
			"id":          newDeviceID(),
			"author":      map[string]interface{}{"role": "user"},
			"create_time": float64(time.Now().UnixMilli()) / 1000,
			"content":     map[string]interface{}{"content_type": "text", "parts": []string{prompt}},
			"metadata":    metadata,
		}},
		"parent_message_id":        newDeviceID(),
		"model":                    model,
		"client_prepare_state":     "sent",
		"timezone_offset_min":      -480,
		"timezone":                 "Asia/Shanghai",
		"conversation_mode":        map[string]interface{}{"kind": "primary_assistant"},
		"enable_message_followups": true,
		"supports_buffering":       true,
		"supported_encodings":      []string{"v1"},
		"client_contextual_info": map[string]interface{}{
			"is_dark_mode":      false,
			"time_since_loaded": 1200,
			"page_height":       1072,
			"page_width":        1724,
			"pixel_ratio":       1.2,
			"screen_height":     1440,
			"screen_width":      2560,
			"app_name":          "chatgpt.com",
		},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
	}
	if len(hints) > 0 {
		body["system_hints"] = hints
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, failure(500, "web_request_encoding_failed")
	}
	response, err := client.send(ctx, http.MethodPost, path,
		client.imageHeader(path, requirements, conduitToken, "text/event-stream"), payload)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		closeBody(response)
		return nil, failure(response.StatusCode, "web_image_submit_failed")
	}
	return response, nil
}

type webImageReferences struct {
	conversationID string
	fileIDs        []string
	sedimentIDs    []string
}

func appendUnique(target []string, values []string) []string {
	for _, value := range values {
		found := false
		for _, existing := range target {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			target = append(target, value)
		}
	}
	return target
}

func webScanReferences(text string, into *webImageReferences) {
	if into.conversationID == "" {
		if match := webConversationRE.FindStringSubmatch(text); len(match) == 2 {
			into.conversationID = match[1]
		}
	}
	for _, match := range webFileServiceRE.FindAllStringSubmatch(text, -1) {
		into.fileIDs = appendUnique(into.fileIDs, []string{match[1]})
	}
	into.fileIDs = appendUnique(into.fileIDs, webImageFileRE.FindAllString(text, -1))
	for _, match := range webSedimentRE.FindAllStringSubmatch(text, -1) {
		into.sedimentIDs = appendUnique(into.sedimentIDs, []string{match[1]})
	}
}

func webReadStream(response *http.Response) webImageReferences {
	defer closeBody(response)
	references := webImageReferences{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), imagesMaxEventBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		webScanReferences(payload, &references)
	}
	return references
}

func (client *webClient) pollConversation(ctx context.Context, references *webImageReferences) error {
	if references.conversationID == "" {
		return failure(502, "web_conversation_missing")
	}
	path := "/backend-api/conversation/" + references.conversationID
	select {
	case <-ctx.Done():
		return failure(499, "web_client_disconnected")
	case <-time.After(webInitialWait):
	}
	deadline := time.Now().Add(webPollBudget)
	for time.Now().Before(deadline) {
		raw, err := client.call(ctx, http.MethodGet, path,
			client.header(path, map[string]string{"Accept": "application/json"}), nil, "web_conversation_read_failed")
		if err == nil {
			webScanReferences(string(raw), references)
			if len(references.fileIDs) > 0 || len(references.sedimentIDs) > 0 {
				return nil
			}
			// Only conclusive while nothing has been produced: a refusal cannot
			// be distinguished from a pending render once assets exist.
			if webPolicyRefusal(webConversationText(raw)) {
				return failure(400, webPolicyCode)
			}
		}
		select {
		case <-ctx.Done():
			return failure(499, "web_client_disconnected")
		case <-time.After(webPollInterval):
		}
	}
	return failure(504, "web_image_not_ready")
}

func (client *webClient) downloadURL(ctx context.Context, path string) string {
	raw, err := client.call(ctx, http.MethodGet, path,
		client.header(path, map[string]string{"Accept": "application/json"}), nil, "web_download_url_failed")
	if err != nil {
		return ""
	}
	var resolved struct {
		DownloadURL string `json:"download_url"`
		URL         string `json:"url"`
	}
	if json.Unmarshal(raw, &resolved) != nil {
		return ""
	}
	if resolved.DownloadURL != "" {
		return resolved.DownloadURL
	}
	return resolved.URL
}

func (client *webClient) fetchImage(ctx context.Context, references webImageReferences) (string, error) {
	urls := []string{}
	for _, fileID := range references.fileIDs {
		if url := client.downloadURL(ctx, "/backend-api/files/"+fileID+"/download"); url != "" {
			urls = appendUnique(urls, []string{url})
		}
	}
	for _, sedimentID := range references.sedimentIDs {
		path := "/backend-api/conversation/" + references.conversationID + "/attachment/" + sedimentID + "/download"
		if url := client.downloadURL(ctx, path); url != "" {
			urls = appendUnique(urls, []string{url})
		}
	}
	if len(urls) == 0 {
		return "", failure(502, "web_download_url_missing")
	}
	lastStatus := 502
	for _, url := range urls {
		blob, status := client.downloadBlob(ctx, url)
		if len(blob) > 0 {
			return base64.StdEncoding.EncodeToString(blob), nil
		}
		if status > 0 {
			lastStatus = status
		}
	}
	return "", failure(lastStatus, "web_download_failed")
}

// downloadBlob mirrors the browser, which carries the full authenticated session
// to the asset host, then retries anonymously because a pre-signed URL rejects an
// unexpected Authorization header.
func (client *webClient) downloadBlob(ctx context.Context, url string) ([]byte, int) {
	if strings.HasPrefix(url, "/") {
		url = webBase + url
	}
	authenticated := client.header("/", map[string]string{"Accept": "*/*"})
	authenticated.Del("X-OpenAI-Target-Path")
	authenticated.Del("X-OpenAI-Target-Route")
	if blob, status := client.getBlob(ctx, url, authenticated); len(blob) > 0 {
		return blob, status
	}
	anonymous := http.Header{}
	anonymous.Set("User-Agent", webBrowserAgent)
	anonymous.Set("Accept", "*/*")
	return client.getBlob(ctx, url, anonymous)
}

func (client *webClient) getBlob(ctx context.Context, url string, header http.Header) ([]byte, int) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0
	}
	request.Header = header
	response, doErr := client.http.Do(request)
	if doErr != nil {
		return nil, 0
	}
	defer closeBody(response)
	blob, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024*1024))
	if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode
	}
	return blob, response.StatusCode
}

func (client *webClient) generate(ctx context.Context, prompt string) (string, error) {
	if err := client.bootstrap(ctx); err != nil {
		return "", err
	}
	requirements, err := client.chatRequirements(ctx)
	if err != nil {
		return "", err
	}
	conduitToken, err := client.prepareConversation(ctx, prompt, requirements, webUpstreamModel, []string{"picture_v2"})
	if err != nil {
		return "", err
	}
	response, err := client.startGeneration(ctx, prompt, requirements, conduitToken, webUpstreamModel, []string{"picture_v2"})
	if err != nil {
		return "", err
	}
	references := webReadStream(response)
	if len(references.fileIDs) == 0 && len(references.sedimentIDs) == 0 {
		if pollErr := client.pollConversation(ctx, &references); pollErr != nil {
			return "", pollErr
		}
	}
	return client.fetchImage(ctx, references)
}

// generateWebImage walks the ChatGPT credentials until one completes a web
// conversation image turn, so an account whose web allowance is spent or whose
// token is stale hands the request to the next one.
func (service *service) generateWebImage(ctx context.Context, callbackID string, request imagesRequest) ([]byte, error) {
	if callbackID == "" {
		return nil, failure(401, "authenticated_execution_callback_required")
	}
	candidates, err := service.imageCandidates(callbackID)
	if err != nil {
		return nil, err
	}
	start := int(nextImageCredential.Add(1)-1) % len(candidates)
	var lastErr error = failure(503, "web_image_unavailable")
	// Each attempt can wait out the full poll budget, so the pool is not walked
	// end to end: a systematic failure would otherwise multiply that wait by the
	// number of accounts.
	attempts := min(len(candidates), webChatMaxCredentials)
	for offset := 0; offset < attempts; offset++ {
		entry := candidates[(start+offset)%len(candidates)]
		token, tokenErr := service.tokenFor(callbackID, entry)
		if tokenErr != nil {
			lastErr = tokenErr
			continue
		}
		client, clientErr := newWebClient(token)
		if clientErr != nil {
			lastErr = clientErr
			continue
		}
		result, generateErr := client.generate(ctx, imagePromptWithHints(request))
		if generateErr != nil {
			// Another account would reproduce the same refusal for the same
			// prompt, so the walk stops rather than spending a second budget.
			if webRefusedByPolicy(generateErr) {
				return nil, generateErr
			}
			lastErr = generateErr
			continue
		}
		return imagesPayload(result, request), nil
	}
	return nil, lastErr
}
