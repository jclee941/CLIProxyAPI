package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"regexp"
	"strings"
)

// webImageModel is an image model this provider publishes and the account mode
// family the web app generates it in: an image made in Flash comes from Nano
// Banana 2, one made in Pro from Nano Banana Pro.
type webImageModel struct {
	ID          string
	DisplayName string
	Family      string
}

var webImageModels = []webImageModel{
	{ID: "gemini-nano-banana-2", DisplayName: "Nano Banana 2 (Gemini)", Family: "Flash"},
	{ID: "gemini-nano-banana-pro", DisplayName: "Nano Banana Pro (Gemini)", Family: "Pro"},
}

func webImageModelFor(id string) (webImageModel, bool) {
	for _, model := range webImageModels {
		if model.ID == id {
			return model, true
		}
	}
	return webImageModel{}, false
}

// webImageCapability finds an account mode by the family in the name the account
// advertises, so "3.8 Flash" serves Flash and "3.5 Flash-Lite" does not.
func webImageCapability(capabilities []capability, family string) (capability, bool) {
	for _, entry := range capabilities {
		if fields := strings.Fields(entry.DisplayName); len(fields) > 1 && strings.Join(fields[1:], " ") == family {
			return entry, true
		}
	}
	return capability{}, false
}

func webImageModelInfos(account accountModels) []modelInfo {
	models := []modelInfo{}
	for _, image := range webImageModels {
		if _, ok := webImageCapability(account.Models, image.Family); ok {
			models = append(models, modelInfo{ID: image.ID, Object: "model", OwnedBy: provider, Type: provider, Name: image.ID, DisplayName: image.DisplayName, Description: "Image generation and editing through the Gemini web app in its " + image.Family + " mode", SupportedGenerationMethods: []string{"generateContent"}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text", "image"}})
		}
	}
	return models
}

// webImagePrompt asks for the image in words, as the web app's client libraries
// do: the model decides from the prompt whether to draw, and a bare description
// of a scene may be answered in prose.
func webImagePrompt(prompt string) string {
	return "Generate an image: " + prompt
}

type webImageRef struct {
	URL string
	ID  string
}

type webImageReply struct {
	Text           string
	ConversationID string
	ReplyID        string
	CandidateID    string
	Images         []webImageRef
}

// webParseImageReply reads the generated images a reply carries in field 7 of
// its rich content, positional or in the sparse bundle.
func webParseImageReply(raw []byte) (webImageReply, error) {
	bodies, err := webResponseFrames(raw)
	if err != nil {
		return webImageReply{}, err
	}
	var reply webImageReply
	for _, body := range bodies {
		candidate := jsonField(body, 4, 0)
		if candidate == nil {
			continue
		}
		if text, ok := jsonField(candidate, 1, 0).(string); ok && text != "" {
			reply.Text = text
		}
		entries, _ := jsonField(webJSPBField(jsonField(candidate, 12), 7), 0).([]any)
		var images []webImageRef
		for _, entry := range entries {
			url, _ := jsonField(entry, 0, 3, 3).(string)
			if !strings.HasPrefix(url, "https://") {
				continue
			}
			id, _ := jsonField(entry, 1, 0).(string)
			images = append(images, webImageRef{URL: url, ID: id})
		}
		if len(images) == 0 {
			continue
		}
		reply.Images = images
		reply.ConversationID, _ = jsonField(body, 1, 0).(string)
		reply.ReplyID, _ = jsonField(body, 1, 1).(string)
		reply.CandidateID, _ = jsonField(candidate, 0).(string)
	}
	return reply, nil
}

// webImageArtifacts are the placeholder links a reply writes where its images go.
var webImageArtifacts = regexp.MustCompile(`https?://googleusercontent\.com/(?:\w+/)+\d+\n*`)

func webImageText(text string) string {
	return strings.TrimSpace(webImageArtifacts.ReplaceAllString(text, ""))
}

// webDownloadImageRPC resolves a generated image's original, the file the web
// app's download button saves.
const webDownloadImageRPC = "c8o8Fe"

// webImageHops bounds the link answers a download gives before the file itself;
// the original answers with a link twice.
const webImageHops = 3

type webImage struct {
	MIMEType        string
	Data            []byte
	OriginalFailure error
}

// imageBytes downloads a generated image at its original size. When that fails
// it fetches the preview at the largest size the product renders, and returns
// the cause with it for the report.
func (session *webSession) imageBytes(ctx context.Context, reply webImageReply, ref webImageRef) (webImage, error) {
	original, err := session.originalImage(ctx, reply, ref)
	if err == nil {
		return original, nil
	}
	mimeType, data, previewErr := session.fetchImage(ctx, webLargestPreview(ref.URL))
	if previewErr != nil {
		return webImage{}, previewErr
	}
	return webImage{MIMEType: mimeType, Data: data, OriginalFailure: err}, nil
}

func (session *webSession) originalImage(ctx context.Context, reply webImageReply, ref webImageRef) (webImage, error) {
	if ref.ID == "" || reply.ConversationID == "" || reply.ReplyID == "" || reply.CandidateID == "" {
		return webImage{}, failure(502, "image_reference_incomplete")
	}
	result, err := session.rpc(ctx, webDownloadImageRPC, []any{
		[]any{[]any{nil, nil, nil, []any{nil, nil, nil, nil, nil, ""}}, []any{ref.ID, 0}, nil, []any{19, ""}, nil, nil, nil, nil, nil, ""},
		[]any{reply.ReplyID, reply.CandidateID, reply.ConversationID, nil, ""},
		1, 0, 1,
	})
	if err != nil {
		return webImage{}, err
	}
	url, _ := jsonField(result, 0).(string)
	if !strings.HasPrefix(url, "https://") {
		return webImage{}, failure(502, "image_download_failed")
	}
	mimeType, data, err := session.fetchImage(ctx, url+"=d-I?alr=yes")
	if err != nil {
		return webImage{}, err
	}
	return webImage{MIMEType: mimeType, Data: data}, nil
}

// fetchImage follows the links a download answers with until it serves the
// image itself.
func (session *webSession) fetchImage(ctx context.Context, url string) (string, []byte, error) {
	for hop := 0; hop <= webImageHops; hop++ {
		status, contentType, body, err := session.download(ctx, url, "image_download_failed")
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusOK {
			return "", nil, failure(502, "image_download_failed")
		}
		mediaType, _, _ := mime.ParseMediaType(contentType)
		if !strings.HasPrefix(mediaType, "image/") {
			mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
		}
		if strings.HasPrefix(mediaType, "image/") {
			return mediaType, body, nil
		}
		next := strings.TrimSpace(string(body))
		if len(next) > 4096 || !strings.HasPrefix(next, "https://") || strings.ContainsAny(next, " \t\r\n") {
			return "", nil, failure(502, "image_download_failed")
		}
		url = next
	}
	return "", nil, failure(502, "image_download_failed")
}

// webLargestPreview asks a preview link for the largest size the product
// renders, 2048 pixels on the long side.
func webLargestPreview(url string) string {
	switch {
	case strings.Contains(url, "=s2048-rj"):
		return url
	case strings.Contains(url, "=s1024-rj"):
		return strings.Replace(url, "=s1024-rj", "=s2048-rj", 1)
	}
	return url + "=s2048-rj"
}

func (service *service) nativeImage(ctx context.Context, record storageRecord, token sessionToken, image webImageModel, payload []byte) ([]byte, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return nil, err
	}
	prompt, media, err := webContentsToPrompt(payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, failure(400, "image_prompt_required")
	}
	session := service.newSession(credential)
	service.trackJar(record.TokenRef, session)
	defer service.persistJar(record.TokenRef, session)
	account, err := session.webCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	model, ok := webImageCapability(account.Capabilities, image.Family)
	if !ok {
		return nil, failure(404, "account_model_unavailable")
	}
	sources, err := service.mediaSources(ctx, media, "")
	if err != nil {
		return nil, err
	}
	attachments, err := session.uploadSources(ctx, sources)
	if err != nil {
		return nil, err
	}
	raw, err := session.sendTurn(ctx, webImagePrompt(prompt), account, model, attachments, 0)
	if err != nil {
		return nil, err
	}
	reply, err := webParseImageReply(raw)
	if err != nil {
		return nil, err
	}
	if len(reply.Images) == 0 {
		return nil, webRefusal("no_image_generated", reply.Text)
	}
	parts := []any{}
	if text := webImageText(reply.Text); text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	for _, ref := range reply.Images {
		generated, err := session.imageBytes(ctx, reply, ref)
		if err != nil {
			return nil, err
		}
		if generated.OriginalFailure != nil {
			service.report(map[string]any{"provider": provider, "state": "image_preview", "reason": generated.OriginalFailure.Error()}, "gemini-web: original image unavailable, the preview was sent")
		}
		parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": generated.MIMEType, "data": base64.StdEncoding.EncodeToString(generated.Data)}})
	}
	encoded, err := json.Marshal(map[string]any{
		"modelVersion": image.ID,
		"candidates":   []any{map[string]any{"index": 0, "finishReason": "STOP", "content": map[string]any{"role": "model", "parts": parts}}},
	})
	if err != nil {
		return nil, failure(500, "web_response_invalid")
	}
	return encoded, nil
}
