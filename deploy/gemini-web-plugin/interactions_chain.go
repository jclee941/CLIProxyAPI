package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
)

const previousVideoDeclaration = "[# Sources <PREVIOUS_VIDEO>@Video1] "

// uploadedVideoDeclaration names the first attached video as the one to extend.
const uploadedVideoDeclaration = "[# Sources <VIDEO_0>@Video1] "

// chainedScheme marks a reference the plugin resolved for itself. A caller
// cannot supply one: the interactions surface accepts only Drive and Files
// references, and the upload checks the caller again before reading a byte.
const chainedScheme = "interaction:"

// chainedLocation names where a previous interaction's video is held: the
// account, the turn, and the caller it belongs to.
type chainedLocation struct {
	Account string
	Key     string
	Caller  string
}

func (location chainedLocation) String() string {
	return chainedScheme + strings.Join([]string{location.Account, location.Key, location.Caller}, "|")
}

func parseChainedReference(value string) (chainedLocation, bool) {
	if !strings.HasPrefix(value, chainedScheme) {
		return chainedLocation{}, false
	}
	fields := strings.Split(strings.TrimPrefix(value, chainedScheme), "|")
	if len(fields) != 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
		return chainedLocation{}, false
	}
	return chainedLocation{Account: fields[0], Key: fields[1], Caller: fields[2]}, true
}

// locateChained finds the account holding a previous interaction when it is not
// the one serving this turn. The owner continues its own conversation, the only
// extension with no length ceiling; another account serves only because the
// owner is blocked, and it continues from the stored video instead. The search
// reads the session store rather than the host's accounts, because a disabled
// owner is one of the blocked owners whose video has to travel.
func (service *service) locateChained(request executorRequest, previous string) (chainedLocation, bool, error) {
	key := continuationKey(previous)
	caller := request.Metadata.CallerScope
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return chainedLocation{}, false, err
	}
	if service.holdsInteraction(record.TokenRef, key, caller) {
		return chainedLocation{}, false, nil
	}
	records, err := service.localStore().records()
	if err != nil {
		return chainedLocation{}, false, err
	}
	for _, local := range records {
		account := local.Target.TokenRef
		if account != record.TokenRef && service.holdsInteraction(account, key, caller) {
			return chainedLocation{Account: account, Key: key, Caller: caller}, true, nil
		}
	}
	// No account holds it, which is also what a caller presenting someone else's
	// interaction looks like. Leaving the token in place keeps that answer where
	// it already lives, on the path that refuses to submit anything for it.
	return chainedLocation{}, false, nil
}

// holdsInteraction reports whether one account holds a finished interaction with
// its video, made by the caller now asking for it. The caller check is what
// keeps one caller's chain out of another's.
func (service *service) holdsInteraction(account, key, caller string) bool {
	local, err := service.localStore().read(account)
	if err != nil {
		return false
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return false
	}
	turn, found := turns[key]
	return found && turn.CallerScope == caller && turn.State == "complete" && turn.ResultStored
}

// chainedSource reads the video a previous interaction produced so it can be
// uploaded to whichever account is serving now.
func (service *service) chainedSource(location chainedLocation) (webSource, error) {
	local, err := service.localStore().read(location.Account)
	if err != nil {
		return webSource{}, err
	}
	stored, err := service.localStore().readInteractionResult(local, location.Key, location.Caller)
	if err != nil {
		return webSource{}, err
	}
	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(stored, &body) != nil || len(body.Candidates) != 1 || len(body.Candidates[0].Content.Parts) != 1 {
		return webSource{}, failure(502, "chained_interaction_unreadable")
	}
	video := body.Candidates[0].Content.Parts[0].InlineData
	content, err := base64.StdEncoding.DecodeString(video.Data)
	if err != nil || video.MIMEType == "" || len(content) == 0 {
		return webSource{}, failure(502, "chained_interaction_unreadable")
	}
	return webSource{MIMEType: video.MIMEType, Size: int64(len(content)), Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}}, nil
}

// withChainedReference attaches the previous video by name, so the request stays
// small and the bytes stream straight to the upload. It goes first among the
// attachments so an extension can name it <VIDEO_0>; any other turn leaves it
// undeclared, which declares it a reference like every uploaded video.
func withChainedReference(payload []byte, location chainedLocation, extend bool) ([]byte, error) {
	content, turn, first, err := userPromptTurn(payload)
	if err != nil {
		return nil, err
	}
	if prompt := first["text"].(string); extend && !webRoleDeclared(prompt) {
		first["text"] = uploadedVideoDeclaration + prompt
	}
	parts := turn["parts"].([]any)
	carried := map[string]any{"fileData": map[string]string{"fileUri": location.String()}}
	turn["parts"] = append([]any{parts[0], carried}, parts[1:]...)
	return json.Marshal(content)
}

// withPreviousVideoDeclaration names the previous turn's video as this turn's
// source. A caller who wrote their own role means it and is left alone.
func withPreviousVideoDeclaration(payload []byte) ([]byte, error) {
	content, _, first, err := userPromptTurn(payload)
	if err != nil {
		return nil, err
	}
	prompt := first["text"].(string)
	if webRoleDeclared(prompt) {
		return payload, nil
	}
	first["text"] = previousVideoDeclaration + prompt
	return json.Marshal(content)
}

// userPromptTurn opens the single user turn and its leading prompt part.
func userPromptTurn(payload []byte) (map[string]any, map[string]any, map[string]any, error) {
	var content map[string]any
	if json.Unmarshal(payload, &content) != nil {
		return nil, nil, nil, failure(500, "chained_interaction_unreadable")
	}
	contents, ok := content["contents"].([]any)
	if !ok || len(contents) != 1 {
		return nil, nil, nil, failure(500, "chained_interaction_unreadable")
	}
	turn, ok := contents[0].(map[string]any)
	if !ok {
		return nil, nil, nil, failure(500, "chained_interaction_unreadable")
	}
	parts, ok := turn["parts"].([]any)
	if !ok || len(parts) == 0 {
		return nil, nil, nil, failure(500, "chained_interaction_unreadable")
	}
	first, ok := parts[0].(map[string]any)
	if !ok {
		return nil, nil, nil, failure(500, "chained_interaction_unreadable")
	}
	if _, written := first["text"].(string); !written {
		return nil, nil, nil, failure(500, "chained_interaction_unreadable")
	}
	return content, turn, first, nil
}
