package main

import (
	_ "embed"
	"encoding/json"
	"testing"
)

//go:embed testdata/flow_models.json
var flowCapturedModelCatalog []byte

func flowModelFixture(t *testing.T) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(flowCapturedModelCatalog, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
