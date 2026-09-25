package main

import (
	_ "embed"
	"net/http"
)

// openapiDocument travels inside the artifact so the description of this surface
// cannot drift from the build serving it, and so it survives a host that has no
// asset directory mounted.
//
//go:embed openapi.json
var openapiDocument []byte

func openapiResource() httpResponse {
	return httpResponse{
		StatusCode: 200,
		Headers: http.Header{
			"Content-Type":           {"application/json; charset=utf-8"},
			"Cache-Control":          {"no-store"},
			"X-Content-Type-Options": {"nosniff"},
		},
		Body: openapiDocument,
	}
}
