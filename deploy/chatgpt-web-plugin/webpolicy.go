package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// webPolicyCode is the refusal the caller sees instead of a poll timeout.
const webPolicyCode = "content_policy_violation"

// A refused image request comes back as an ordinary assistant message, which
// leaves the conversation without a file reference and so looks exactly like an
// image still being rendered. The list is the sidecar's, kept verbatim so both
// paths refuse the same texts.
var webPolicyKeywords = []string{
	"内容政策", "防护限制", "违反", "moderation", "policy", "blocked",
	"may violate our guardrails",
	"不能生成", "无法生成", "不能帮助", "无法帮助",
	"裸体", "裸露", "色情", "性内容", "未成年",
	"抱歉，我不能",
}

func webPolicyRefusal(text string) bool {
	if text == "" {
		return false
	}
	lowered := strings.ToLower(text)
	for _, keyword := range webPolicyKeywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}
	return false
}

// A finished image arrives as a non-string part, so reading only string parts
// keeps a delivered image from being scanned as a refusal.
func webConversationText(raw []byte) string {
	var document struct {
		Mapping map[string]struct {
			Message *struct {
				Author struct {
					Role string `json:"role"`
				} `json:"author"`
				Content struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
			} `json:"message"`
		} `json:"mapping"`
	}
	if json.Unmarshal(raw, &document) != nil {
		return ""
	}
	var builder strings.Builder
	for _, node := range document.Mapping {
		if node.Message == nil || node.Message.Author.Role != "assistant" {
			continue
		}
		for _, part := range node.Message.Content.Parts {
			var text string
			if json.Unmarshal(part, &text) != nil || text == "" {
				continue
			}
			builder.WriteString(text)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// webRefusedByPolicy reports whether an attempt already established that the
// account refuses this prompt, so the remaining credentials are not asked to
// reproduce the same refusal.
func webRefusedByPolicy(err error) bool {
	var public *publicError
	return errors.As(err, &public) && public.Code == webPolicyCode
}
