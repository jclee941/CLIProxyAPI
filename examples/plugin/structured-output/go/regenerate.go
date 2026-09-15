package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// defaultMaxAttempts bounds how many times a violating reply is regenerated.
const defaultMaxAttempts = 2

// enforcement carries one request's contract together with everything needed to
// ask the model again when a reply breaks it.
type enforcement struct {
	spec         *outputSpec
	tools        *toolSpec
	cfg          pluginConfig
	sourceFormat string
	model        string
	request      []byte
}

// apply returns the body to deliver downstream and whether it changed. The reply
// is reduced to its JSON value and checked against the contract; one that still
// violates it is regenerated with the specific errors until it conforms or the
// attempt budget is spent. The best reply obtained is delivered either way, so
// enforcement never turns a usable answer into a failed request.
func (e enforcement) apply(body []byte) ([]byte, bool) {
	changed := false
	for attempt := 0; ; attempt++ {
		path := replyTextPath(body)
		if path == "" {
			return body, changed
		}
		raw := gjson.GetBytes(body, path).String()
		text := raw
		if e.cfg.StripAgentTags {
			text = stripAgentTags(text)
		}
		candidate, extracted := "", false
		if e.enforcing() {
			candidate, extracted = extractJSON(text)
			if extracted && e.cfg.Clean && e.spec != nil {
				text = candidate
			}
		}
		if text != raw {
			if updated, errSet := sjson.SetBytes(body, path, text); errSet == nil {
				body, changed = updated, true
			}
		}
		if !e.enforcing() || !e.cfg.Validate {
			return body, changed
		}
		violations := e.violations(candidate, extracted)
		if len(violations) == 0 {
			if settled, ok := e.settle(body, candidate, extracted); ok {
				return settled, true
			}
			return body, changed
		}
		if attempt >= e.cfg.MaxAttempts {
			return body, changed
		}
		regenerated, ok := e.regenerate(raw, violations)
		if !ok {
			return body, changed
		}
		body, changed = regenerated, true
	}
}

// enforcing reports whether this request carries a contract worth holding.
func (e enforcement) enforcing() bool {
	return e.spec != nil || e.tools.demandsCall()
}

func (e enforcement) violations(candidate string, extracted bool) []string {
	if e.tools.demandsCall() {
		return toolViolations(e.tools, candidate, extracted)
	}
	return replyViolations(e.spec, candidate, extracted)
}

// settle converts a conforming tool envelope into the native tool call the
// caller asked for. A plain structured reply needs no such step.
func (e enforcement) settle(body []byte, candidate string, extracted bool) ([]byte, bool) {
	if !e.tools.demandsCall() || !extracted {
		return body, false
	}
	calls, ok := parseToolEnvelope(candidate)
	if !ok {
		return body, false
	}
	return writeToolCalls(body, calls)
}

func (e enforcement) regenerate(reply string, violations []string) ([]byte, bool) {
	corrected, ok := appendCorrection(nonStreaming(e.request), reply, e.correction(violations))
	if !ok {
		return nil, false
	}
	body, ok := executeOnce(e.sourceFormat, e.model, corrected)
	if !ok {
		return nil, false
	}
	if replyTextPath(body) == "" && !hasNativeToolCall(body) {
		return nil, false
	}
	return body, true
}

func (e enforcement) correction(violations []string) string {
	lines := []string{"The previous reply did not satisfy the requested output contract:"}
	for _, violation := range violations {
		lines = append(lines, "- "+violation)
	}
	return strings.Join(append(lines, "", e.contract()), "\n")
}

func (e enforcement) contract() string {
	if e.tools.demandsCall() {
		return toolInstructionText(e.tools)
	}
	return instructionText(e.spec)
}

func replyViolations(spec *outputSpec, candidate string, extracted bool) []string {
	if !extracted {
		return []string{"the reply is not a JSON value"}
	}
	return validateValue(spec, candidate)
}

// executeOnce runs one non-streaming model call through the host. The host stamps
// this plugin's identity onto the callback, so the nested execution skips this
// plugin's interceptors and cannot recurse.
func executeOnce(sourceFormat, model string, body []byte) ([]byte, bool) {
	raw, errCall := callHost(pluginabi.MethodHostModelExecute, pluginapi.HostModelExecutionRequest{
		EntryProtocol: sourceFormat,
		ExitProtocol:  sourceFormat,
		Model:         model,
		Stream:        false,
		Body:          body,
	})
	if errCall != nil {
		return nil, false
	}
	var resp pluginapi.HostModelExecutionResponse
	if json.Unmarshal(raw, &resp) != nil {
		return nil, false
	}
	if resp.StatusCode != 0 && resp.StatusCode != http.StatusOK {
		return nil, false
	}
	if len(resp.Body) == 0 {
		return nil, false
	}
	return resp.Body, true
}

// enforceResponse holds the contract for one completed response and reports
// whether the body changed.
func enforceResponse(req pluginapi.ResponseInterceptRequest, cfg pluginConfig) ([]byte, bool) {
	holder := enforcement{
		spec:         parseSpec(req.OriginalRequest),
		tools:        parseTools(req.OriginalRequest),
		cfg:          cfg,
		sourceFormat: req.SourceFormat,
		model:        req.Model,
		request:      req.OriginalRequest,
	}
	if !holder.enforcing() && !cfg.StripAgentTags {
		return req.Body, false
	}
	return holder.apply(req.Body)
}

// nonStreaming clears a streaming flag the replayed request must not carry: a
// reply can only be checked once it is complete.
func nonStreaming(payload []byte) []byte {
	if !gjson.GetBytes(payload, "stream").Exists() {
		return payload
	}
	updated, err := sjson.SetBytes(payload, "stream", false)
	if err != nil {
		return payload
	}
	return updated
}
