package main

import (
	"context"
	"mime"
	"net/http"
	"strings"
)

// webMusicModel is the music model this provider publishes; the web app makes
// the track with Lyria from a turn in its Flash mode.
const webMusicModel = "gemini-lyria-3.5"

func webMusicModelInfos(account accountModels) []modelInfo {
	if _, ok := webFamilyCapability(account.Models, "Flash"); !ok {
		return nil
	}
	return []modelInfo{{ID: webMusicModel, Object: "model", OwnedBy: provider, Type: provider, Name: webMusicModel, DisplayName: "Lyria 3.5 (Gemini)", Description: "Music generation through the Gemini web app; returns the track and the web app's cover video", SupportedGenerationMethods: []string{"generateContent"}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text", "audio", "video"}}}
}

func webMusicPrompt(prompt string) string {
	return "Generate music: " + prompt
}

type webMusicReply struct {
	Text  string
	Audio string
	Video string
}

// webParseMusicReply reads the track a reply carries in field 86 of its rich
// content: the mp3 at [0][1][7] and the cover video at [1][1][7], each a pair of
// a thumbnail link and the file link.
func webParseMusicReply(raw []byte) (webMusicReply, error) {
	bodies, err := webResponseFrames(raw)
	if err != nil {
		return webMusicReply{}, err
	}
	var reply webMusicReply
	for _, body := range bodies {
		candidate := jsonField(body, 4, 0)
		if candidate == nil {
			continue
		}
		if text, ok := jsonField(candidate, 1, 0).(string); ok && text != "" {
			reply.Text = text
		}
		media := webJSPBField(jsonField(candidate, 12), 86)
		if audio, _ := jsonField(media, 0, 1, 7, 1).(string); strings.HasPrefix(audio, "https://") {
			reply.Audio = audio
		}
		if video, _ := jsonField(media, 1, 1, 7, 1).(string); strings.HasPrefix(video, "https://") {
			reply.Video = video
		}
	}
	return reply, nil
}

// webMediaWaits bounds how many times a generated file still being written is
// asked for again; its download answers 206 until the file is complete.
const webMediaWaits = 30

func (service *service) fetchMedia(ctx context.Context, session *webSession, url, kind string) (string, []byte, error) {
	for attempt := 0; ; attempt++ {
		status, contentType, body, err := session.download(ctx, url, kind+"_download_failed")
		if err != nil {
			return "", nil, err
		}
		if status == http.StatusOK {
			mediaType, _, _ := mime.ParseMediaType(contentType)
			if !strings.HasPrefix(mediaType, kind+"/") {
				mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
			}
			if !strings.HasPrefix(mediaType, kind+"/") {
				return "", nil, failure(502, kind+"_download_failed")
			}
			return mediaType, body, nil
		}
		if status != http.StatusPartialContent || attempt >= webMediaWaits {
			return "", nil, failure(502, kind+"_download_failed")
		}
		if err := service.waitInteraction(ctx); err != nil {
			return "", nil, err
		}
	}
}

func (service *service) nativeMusic(ctx context.Context, record storageRecord, token sessionToken, payload []byte) ([]byte, error) {
	session, raw, err := service.webMediaTurn(ctx, record, token, "Flash", "music", webMusicPrompt, payload)
	if session != nil {
		defer service.persistJar(record.TokenRef, session)
	}
	if err != nil {
		return nil, err
	}
	if service.settings().DeleteChats {
		defer service.deleteChat(ctx, session, webReplyConversation(raw))
	}
	reply, err := webParseMusicReply(raw)
	if err != nil {
		return nil, err
	}
	if reply.Audio == "" && reply.Video == "" {
		return nil, webRefusal("no_music_generated", reply.Text)
	}
	parts := []any{}
	if text := webImageText(reply.Text); text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	for _, file := range []struct{ url, kind string }{{reply.Audio, "audio"}, {reply.Video, "video"}} {
		if file.url == "" {
			continue
		}
		mimeType, data, err := service.fetchMedia(ctx, session, file.url, file.kind)
		if err != nil {
			return nil, err
		}
		parts = append(parts, webMediaPart(mimeType, data))
	}
	return webMediaResponse(webMusicModel, parts)
}
