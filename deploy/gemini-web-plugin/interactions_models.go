package main

// interactionModels is the whole published surface. A request names the
// official Interactions model while the executor matches it against the
// account's verified capabilities.
func (service *service) interactionModels(account accountModels) []modelInfo {
	models := []modelInfo{}
	// Publication says what this provider offers, not whether a deployment
	// toggle is on: with continuation disabled a request is answered with
	// native_continuation_disabled, which is far easier to act on than a model
	// that silently stopped existing.
	if account.Available {
		models = append(models, modelInfo{ID: interactionOmniModel, Object: "model", OwnedBy: provider, Type: provider, Name: interactionOmniModel, DisplayName: "Gemini Omni 1.1 Flash", Description: "Official Interactions request/response shape for inline text-to-video and stored follow-up turns", SupportedGenerationMethods: []string{"interactions"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"video"}})
	}
	return models
}
