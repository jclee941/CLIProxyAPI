package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The web product has no function calling, so a Gemini request is flattened into
// one prompt and the tool contract is stated in that prompt. Replies are scanned
// back for the block the model was asked to emit. Both directions have to match
// the wording the model was trained against by this bridge, so the text here is
// reproduced rather than rewritten.

type webToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

func webStringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}

func webMapField(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func webListField(value any) []any {
	list, _ := value.([]any)
	return list
}

func webFunctionCallingMode(request map[string]any) string {
	config := webMapField(webMapField(request["toolConfig"])["functionCallingConfig"])
	if mode := webStringField(config, "mode"); mode != "" {
		return mode
	}
	return "AUTO"
}

func webToolDefinitions(request map[string]any) []webToolDefinition {
	if webFunctionCallingMode(request) == "NONE" {
		return nil
	}
	var definitions []webToolDefinition
	for _, group := range webListField(request["tools"]) {
		for _, declaration := range webListField(webMapField(group)["functionDeclarations"]) {
			entry := webMapField(declaration)
			definition := webToolDefinition{Name: webStringField(entry, "name"), Description: webStringField(entry, "description")}
			if parameters, present := entry["parameters"]; present && parameters != nil {
				definition.Parameters = parameters
			} else if schema, present := entry["parametersJsonSchema"]; present && schema != nil {
				definition.Parameters = schema
			}
			definitions = append(definitions, definition)
		}
	}
	return definitions
}

// webToolChoiceInstruction restates functionCallingConfig as prose, because the
// mode cannot be enforced on a chat product.
func webToolChoiceInstruction(request map[string]any) string {
	config := webMapField(webMapField(request["toolConfig"])["functionCallingConfig"])
	switch webFunctionCallingMode(request) {
	case "NONE":
		return "\n\nIMPORTANT: Do NOT call any tools. Respond with text only."
	case "ANY":
		allowed := webListField(config["allowedFunctionNames"])
		if len(allowed) == 0 {
			return "\n\nIMPORTANT: You MUST call at least one tool. Do not respond with text only."
		}
		names := make([]string, 0, len(allowed))
		for _, name := range allowed {
			if text, ok := name.(string); ok {
				names = append(names, `"`+text+`"`)
			}
		}
		return "\n\nIMPORTANT: You MUST call one of these tools: " + strings.Join(names, ", ") + ". Do not respond with text only."
	}
	return ""
}

func webToolPrompt(definitions []webToolDefinition) string {
	encoded, err := json.MarshalIndent(definitions, "", "  ")
	if err != nil {
		return ""
	}
	return "# Tool Use\n\n" +
		"You can call the following tools to help accomplish tasks. " +
		"These tools connect to the user's local environment and will execute when called.\n\n" +
		"Call format (use this exact format):\n" +
		"```function_call\n" +
		`{"name": "<tool_name>", "args": {<arguments>}}` + "\n" +
		"```\n\n" +
		"When calling tools:\n" +
		"- Output ONLY the function_call block(s), nothing else\n" +
		"- You may call multiple tools with multiple blocks\n" +
		"- After receiving a [Tool result for ...], use that data to answer the user\n\n" +
		"Available tools:\n" + string(encoded)
}

// webInlineData reads the media a part carries. The host spells this three ways
// by the time a request reaches here - a Gemini caller sends inlineData with
// mimeType, the OpenAI bridge sends inlineData with mime_type and the Claude
// bridge sends inline_data with mime_type - and all three are valid Gemini REST.
// Reading only one spelling meant an OpenAI image was refused as malformed and a
// Claude image was dropped without a word, which is the worse of the two.
func webInlineData(field map[string]any) (map[string]any, bool) {
	for _, key := range []string{"inlineData", "inline_data"} {
		if value, present := field[key]; present && value != nil {
			return webMapField(value), true
		}
	}
	return nil, false
}

func webInlineMIME(inline map[string]any) string {
	if mime := webStringField(inline, "mimeType"); mime != "" {
		return mime
	}
	return webStringField(inline, "mime_type")
}

// webFileReference reads the uri a part names in place of carrying bytes. It has
// two spellings for the same reason inline media does.
func webFileReference(field map[string]any) (string, bool) {
	for _, key := range []string{"fileData", "file_data"} {
		value, present := field[key]
		if !present || value == nil {
			continue
		}
		data := webMapField(value)
		uri := webStringField(data, "fileUri")
		if uri == "" {
			uri = webStringField(data, "file_uri")
		}
		return uri, uri != ""
	}
	return "", false
}

// webContentsToPrompt flattens a Gemini request into the single prompt the web
// product accepts. It reports whether the request carried inline media, which the
// text path cannot forward.
func webContentsToPrompt(raw []byte) (string, []webMedia, error) {
	var request map[string]any
	if json.Unmarshal(raw, &request) != nil {
		return "", nil, failure(400, "invalid_generation_request")
	}
	var sections []string
	definitions := webToolDefinitions(request)
	instruction := ""
	if len(definitions) > 0 {
		instruction = webToolPrompt(definitions) + webToolChoiceInstruction(request)
	}
	systemText := ""
	for _, part := range webListField(webMapField(request["systemInstruction"])["parts"]) {
		if text := webStringField(webMapField(part), "text"); text != "" {
			systemText = strings.TrimSpace(systemText + " " + text)
		}
	}
	switch {
	case systemText != "" && instruction != "":
		sections = append(sections, systemText+"\n\n"+instruction)
	case systemText != "":
		sections = append(sections, systemText)
	case instruction != "":
		sections = append(sections, instruction)
	}

	media := []webMedia{}
	for _, content := range webListField(request["contents"]) {
		entry := webMapField(content)
		var lines []string
		for _, part := range webListField(entry["parts"]) {
			field := webMapField(part)
			inline, carriesMedia := webInlineData(field)
			reference, namesFile := webFileReference(field)
			switch {
			case webStringField(field, "text") != "":
				lines = append(lines, webStringField(field, "text"))
			case namesFile:
				_, drive := driveFileID(reference)
				_, stored := filesReferenceID(reference)
				if !drive && !stored {
					return "", nil, failure(400, "attachment_reference_unsupported")
				}
				media = append(media, webMedia{Reference: reference})
				lines = append(lines, "[File attached]")
			case carriesMedia:
				mime := webInlineMIME(inline)
				encoded := webStringField(inline, "data")
				if mime == "" || encoded == "" {
					return "", nil, failure(400, "invalid_generation_request")
				}
				media = append(media, webMedia{MIMEType: mime, Data: encoded})
				lines = append(lines, webAttachmentNotice(mime))
			case field["functionCall"] != nil:
				call := webMapField(field["functionCall"])
				encoded, err := json.Marshal(map[string]any{"name": webStringField(call, "name"), "args": call["args"]})
				if err != nil {
					return "", nil, failure(400, "invalid_generation_request")
				}
				lines = append(lines, "```function_call\n"+string(encoded)+"\n```")
			case field["functionResponse"] != nil:
				response := webMapField(field["functionResponse"])
				encoded, err := json.Marshal(response["response"])
				if err != nil {
					return "", nil, failure(400, "invalid_generation_request")
				}
				lines = append(lines, "[Tool result for "+webStringField(response, "name")+"]: "+string(encoded))
			}
		}
		text := strings.Join(lines, "\n")
		if webStringField(entry, "role") == "model" {
			text = "[Assistant]: " + text
		}
		if strings.TrimSpace(text) != "" {
			sections = append(sections, text)
		}
	}
	return strings.Join(sections, "\n\n"), media, nil
}

var (
	webFencedCallPattern   = regexp.MustCompile("(?s)```function_call\\s*\\n(.*?)\\n```")
	webUnfencedCallPattern = regexp.MustCompile("(?s)(?:^|\\n)function_call\\s*\\n(\\{[^`]*?\\})")
)

type webFunctionCall struct {
	Name string
	Args any
}

// webParseFunctionCalls recovers the calls from a reply. Three shapes are
// accepted because the model reproduces the requested block loosely: fenced,
// unfenced, and a bare object when the whole reply is the call.
func webParseFunctionCalls(text string) (string, []webFunctionCall) {
	clean := text
	var calls []webFunctionCall
	for _, pattern := range []*regexp.Regexp{webFencedCallPattern, webUnfencedCallPattern} {
		for _, match := range pattern.FindAllStringSubmatch(clean, -1) {
			if call, ok := webDecodeCall(match[1]); ok {
				calls = append(calls, call)
			}
		}
		clean = strings.TrimSpace(pattern.ReplaceAllString(clean, ""))
	}
	if len(calls) == 0 && strings.HasPrefix(strings.TrimSpace(clean), "{") {
		if call, ok := webDecodeCall(clean); ok {
			calls = append(calls, call)
			clean = ""
		}
	}
	return clean, calls
}

func webDecodeCall(raw string) (webFunctionCall, bool) {
	var decoded map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &decoded) != nil {
		return webFunctionCall{}, false
	}
	name := webStringField(decoded, "name")
	if name == "" {
		return webFunctionCall{}, false
	}
	arguments, present := decoded["args"]
	if !present {
		arguments = decoded["arguments"]
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return webFunctionCall{Name: name, Args: arguments}, true
}
