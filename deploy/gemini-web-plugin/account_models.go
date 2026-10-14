package main

import (
	"context"
	"net/http"
)

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, MaxIdleConns: 10, MaxIdleConnsPerHost: 10, DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type capability struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name"`
	Mode         int    `json:"mode"`
}
type accountModels struct {
	AccountSHA256 string       `json:"account_sha256,omitempty"`
	Available     bool         `json:"available"`
	StatusCode    *int         `json:"status_code"`
	Models        []capability `json:"models"`
	ObservedAt    float64      `json:"observed_at"`
	CapacityFlags []int        `json:"capacity_flags,omitempty"`
	Features      []int        `json:"features,omitempty"`
}

// accountModels reports what an account can serve by asking the web product
// directly, so an account's health depends on nothing but its own session.
func (service *service) accountModels(ctx context.Context, reference string, token sessionToken) (accountModels, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return accountModels{}, err
	}
	session := service.newSession(credential)
	service.trackJar(reference, session)
	defer service.persistJar(reference, session)
	account, err := session.webCapabilities(ctx)
	if err != nil {
		return accountModels{}, err
	}
	return accountModels{Available: true, Models: account.Capabilities, ObservedAt: float64(service.now().Unix()), CapacityFlags: account.CapacityFlags, Features: account.Features}, nil
}

func verifiedModels(account accountModels) []modelInfo {
	models := make([]modelInfo, 0, 2)
	if !account.Available {
		return models
	}
	matches := 0
	for _, capability := range account.Models {
		if capability.DisplayName == "3.8 Flash" && capability.CapabilityID != "" {
			matches++
		}
	}
	if matches == 1 {
		models = append(models, modelInfo{ID: flashModel, Object: "model", OwnedBy: provider, Type: provider, Name: flashModel, DisplayName: "Gemini Web Flash 3.8", Description: "Account-verified 3.8 Flash; text and tool emulation; buffered streaming", SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"text"}})
	}
	models = append(models, modelInfo{ID: omniModel, Object: "model", OwnedBy: provider, Type: provider, Name: omniModel, DisplayName: "Gemini Web Omni", Description: "Web video tool; synchronous Gemini-native single text turn only; availability determined at execution", SupportedGenerationMethods: []string{"generateContent"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"video"}})
	return models
}
