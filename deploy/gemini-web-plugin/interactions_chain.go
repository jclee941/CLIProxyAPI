package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
)

// A follow-up names the interaction it continues, but the previous video's
// bytes are the reference the next account needs. Keeping the product
// conversation pinned to the account that produced it serialises a whole chain
// onto one cooldown. Carrying the stored video instead lets the scheduler pick
// any account.

// chainedScheme marks a reference the plugin resolved for itself. A caller
// cannot supply one: every route that accepts a file reference resolves it as a
// Drive file first and rejects what it cannot place.
const chainedScheme = "interaction:"

// chainedLocation names where a previous interaction's video is held: the
// account, the turn, and the caller it belongs to. The caller travels with it
// because the read checks it again at the file rather than trusting the name.
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

// locateChained finds the account holding a previous interaction. The result is
// used as an attachment even when the serving account also holds it, so the
// follow-up never becomes account-pinned conversation state.
func (service *service) locateChained(request executorRequest, previous string) (chainedLocation, bool, error) {
	key := continuationKey(previous)
	caller := request.Metadata.CallerScope
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return chainedLocation{}, false, err
	}
	if service.holdsInteraction(record.TokenRef, key, caller) {
		return chainedLocation{Account: record.TokenRef, Key: key, Caller: caller}, true, nil
	}
	entries, err := service.entries(request.HostCallbackID)
	if err != nil {
		return chainedLocation{}, false, err
	}
	for _, entry := range entries {
		other, found, recordErr := service.getRecord(request.HostCallbackID, entry)
		// One unreadable account does not decide the answer for the rest.
		if recordErr != nil || !found || other.TokenRef == record.TokenRef {
			continue
		}
		if service.holdsInteraction(other.TokenRef, key, caller) {
			return chainedLocation{Account: other.TokenRef, Key: key, Caller: caller}, true, nil
		}
	}
	// No account holds it, which is also what a caller presenting someone else's
	// interaction looks like. Leaving the token in place keeps that answer where
	// it already lives, on the path that refuses to submit anything for it.
	return chainedLocation{}, false, nil
}

// holdsInteraction reports whether one account holds a finished interaction with
// its video, made by the caller now asking for it. The caller check is the only
// thing keeping one caller's chain out of another's.
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

// withChainedReference attaches the previous video to the turn being submitted.
// It names the file rather than carrying its bytes, so the request stays the
// size it was and the video is streamed straight to the upload.
func withChainedReference(payload []byte, location chainedLocation) ([]byte, error) {
	var content map[string]any
	if json.Unmarshal(payload, &content) != nil {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	contents, ok := content["contents"].([]any)
	if !ok || len(contents) != 1 {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	turn, ok := contents[0].(map[string]any)
	if !ok {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	parts, ok := turn["parts"].([]any)
	if !ok {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	turn["parts"] = append(parts, map[string]any{"fileData": map[string]string{"fileUri": location.String()}})
	return json.Marshal(content)
}
