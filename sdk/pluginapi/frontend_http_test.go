package pluginapi

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestFrontendHTTPWireContract(t *testing.T) {
	raw, err := json.Marshal(FrontendHTTPRequest{Body: []byte{0, 255}, CallerScope: "trusted", Params: map[string]string{"id": "file-1"}})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["caller_scope"] != "trusted" || wire["Body"] != "AP8=" || wire["Params"].(map[string]any)["id"] != "file-1" {
		t.Fatal("frontend RPC fields changed")
	}
	if base64.StdEncoding.EncodedLen(FrontendHTTPMaxBodyBytes)+(1<<20) >= FrontendHTTPMaxMessageBytes {
		t.Fatal("body bound leaves no headroom for native RPC metadata")
	}
	raw, err = json.Marshal(FrontendHTTPRegistrationResponse{Routes: []FrontendHTTPRoute{{Method: "GET", Path: "/files/{id}"}}})
	if err != nil {
		t.Fatal(err)
	}
	var registration struct{ Routes []map[string]any }
	if err := json.Unmarshal(raw, &registration); err != nil {
		t.Fatal(err)
	}
	if _, present := registration.Routes[0]["Handler"]; present {
		t.Fatal("Go handler leaked into native route registration")
	}
}
