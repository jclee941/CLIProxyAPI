package main

import "strings"

// The web conversation takes a prompt and nothing else, so the sidecar carried
// size and quality as sentences appended to it. Porting the request shape
// without these sentences silently dropped both: a caller could ask for a
// portrait image and always receive the default framing.
func imagePromptWithHints(request imagesRequest) string {
	prompt := strings.TrimSpace(request.Prompt)
	hints := make([]string, 0, 2)
	if size := strings.TrimSpace(request.Size); size != "" {
		hints = append(hints, "Render the image at "+size+".")
	}
	if quality := strings.TrimSpace(request.Quality); quality != "" && quality != "auto" {
		hints = append(hints, "Render the image at "+quality+" quality.")
	}
	if len(hints) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(hints, " ")
}
