package main

import "testing"

const personSchema = `{
	"type":"object",
	"properties":{
		"name":{"type":"string"},
		"age":{"type":"integer"},
		"role":{"type":"string","enum":["admin","user"]}
	},
	"required":["name","age"],
	"additionalProperties":false
}`

func TestValidateValueAcceptsConformingReply(t *testing.T) {
	spec := &outputSpec{Kind: "json_schema", Schema: personSchema}
	if violations := validateValue(spec, `{"name":"ada","age":36,"role":"admin"}`); len(violations) != 0 {
		t.Fatalf("conforming reply reported violations: %v", violations)
	}
}

func TestValidateValueReportsContractBreaches(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"missing required", `{"name":"ada"}`, "age is required but missing"},
		{"wrong type", `{"name":"ada","age":"36"}`, "age must be integer but is string"},
		{"undeclared property", `{"name":"ada","age":36,"extra":true}`, "extra is not a declared property"},
		{"outside enum", `{"name":"ada","age":36,"role":"root"}`, `role must be one of ["admin","user"]`},
		{"wrong root type", `["ada"]`, "the reply must be object but is array"},
		{"not json at all", `I cannot do that`, "the reply is not a JSON value"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			violations := validateValue(&outputSpec{Kind: "json_schema", Schema: personSchema}, testCase.value)
			if !containsString(violations, testCase.want) {
				t.Fatalf("violations %v do not contain %q", violations, testCase.want)
			}
		})
	}
}

func TestValidateValueFollowsReferencesAndVariants(t *testing.T) {
	spec := &outputSpec{Kind: "json_schema", Schema: `{
		"type":"object",
		"properties":{
			"items":{"type":"array","items":{"$ref":"#/$defs/item"}},
			"note":{"anyOf":[{"type":"string"},{"type":"null"}]}
		},
		"required":["items"],
		"additionalProperties":false,
		"$defs":{"item":{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}}
	}`}
	if violations := validateValue(spec, `{"items":[{"id":1}],"note":null}`); len(violations) != 0 {
		t.Fatalf("conforming reply reported violations: %v", violations)
	}
	violations := validateValue(spec, `{"items":[{"id":"one"}],"note":7}`)
	for _, want := range []string{"items[0].id must be integer but is string", "note does not match any permitted variant"} {
		if !containsString(violations, want) {
			t.Fatalf("violations %v do not contain %q", violations, want)
		}
	}
}

func TestValidateValueChecksJSONObjectRequests(t *testing.T) {
	spec := &outputSpec{Kind: "json_object"}
	if violations := validateValue(spec, `{"a":1}`); len(violations) != 0 {
		t.Fatalf("object reply reported violations: %v", violations)
	}
	if violations := validateValue(spec, `[1,2]`); len(violations) == 0 {
		t.Fatal("a non-object reply must violate a json_object request")
	}
}

func TestValidateValueIgnoresUnreadableSchema(t *testing.T) {
	spec := &outputSpec{Kind: "json_schema", Schema: `this is not a schema`}
	if violations := validateValue(spec, `{"a":1}`); len(violations) != 0 {
		t.Fatalf("a schema the plugin cannot read must not fail a reply: %v", violations)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
