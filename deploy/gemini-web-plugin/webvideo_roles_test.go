package main

import (
	"strings"
	"testing"
)

// The role tags are the product's own prompt syntax and the only way a caller
// names a role on this surface. Every spelling has to survive: the frame tags
// used to be overwritten because the check only knew the two tags this function
// injects itself, so a caller asking for a starting frame with a video attached
// had their prompt rewritten into a reference declaration.
func TestEveryCallerWrittenRoleIsLeftAlone(t *testing.T) {
	for _, written := range []string{
		"<FIRST_FRAME> a woman walking",
		"<FIRST_FRAME> <LAST_FRAME> a woman walking",
		"in the style of <IMAGE_REF_0> a kite",
		"[# Sources <FIRST_FRAME>@Image1] a kite",
	} {
		t.Run(written, func(t *testing.T) {
			got := webReferenceDeclaration(written, []webAttachment{{MIMEType: "image/png"}, {MIMEType: "video/mp4"}})
			if got != written {
				t.Fatalf("a written role was rewritten: %s", got)
			}
		})
	}
}

// An image stays undeclared because the tool already takes it as a starting
// frame; saying otherwise would contradict what is measured to work.
func TestOnlyAVideoIsDeclared(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		attachments []webAttachment
		declared    bool
	}{
		{"one image", []webAttachment{{MIMEType: "image/png"}}, false},
		{"a document", []webAttachment{{MIMEType: "application/pdf"}}, false},
		{"no attachments", nil, false},
		{"one video", []webAttachment{{MIMEType: "video/mp4"}}, true},
		{"an image and a video", []webAttachment{{MIMEType: "image/png"}, {MIMEType: "video/mp4"}}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := webReferenceDeclaration("a kite over a beach", scenario.attachments)
			if declared := strings.Contains(got, "[# References"); declared != scenario.declared {
				t.Fatalf("declared=%t want %t: %s", declared, scenario.declared, got)
			}
		})
	}
}
