package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFlowOutputReferencesSurviveEveryNativeFormat(t *testing.T) {
	// Given: two outputs whose IDs are needed by later edit/extend requests.
	body := []byte(`{"modelVersion":"flow-test","candidates":[
{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"AQID"},"flow":{"mediaId":"image-a","projectId":"project-a"}}]}},
{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"BAUG"},"flow":{"mediaId":"video-b","projectId":"project-a"}}]}}]}`)
	for _, format := range []string{"openai", "openai-response", "claude"} {
		for _, stream := range []bool{false, true} {
			t.Run(format+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				// When: the caller's native JSON or event stream is emitted.
				result, err := flowExecutionResult(body, format, stream)
				if err != nil {
					t.Fatal(err)
				}
				payload, chunks, _ := flowOutTestWire(t, result)
				if !stream {
					chunks = [][]byte{payload}
				}

				// Then: an event carries the stable IDs and candidate mapping.
				found := false
				for _, chunk := range chunks {
					for _, line := range bytes.Split(chunk, []byte("\n")) {
						line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
						if len(line) == 0 || line[0] != '{' {
							continue
						}
						var value struct {
							Flow     []flowOutMediaReference `json:"flow"`
							Response *struct {
								Flow []flowOutMediaReference `json:"flow"`
							} `json:"response"`
							Message *struct {
								Flow []flowOutMediaReference `json:"flow"`
							} `json:"message"`
						}
						if err := json.Unmarshal(line, &value); err != nil {
							t.Fatal(err)
						}
						references := value.Flow
						if value.Response != nil {
							references = value.Response.Flow
						}
						if value.Message != nil {
							references = value.Message.Flow
						}
						if len(references) == 0 {
							continue
						}
						if len(references) != 2 || references[0].MediaID != "image-a" ||
							references[1].MediaID != "video-b" || references[1].ProjectID != "project-a" ||
							references[1].Candidate != 1 || references[1].Part != 0 {
							t.Fatalf("lost references: %+v", references)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("no output references")
				}
			})
		}
	}
}
