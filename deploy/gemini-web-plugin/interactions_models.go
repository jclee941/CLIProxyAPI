package main

func (service *service) interactionModels(account accountModels) []modelInfo {
	models := verifiedModels(account)
	if account.Available && service.settings().NativeContinuation && service.settings().NativeGeneration {
		models = append(models, modelInfo{ID: interactionOmniModel, Object: "model", OwnedBy: provider, Type: provider, Name: interactionOmniModel, DisplayName: "Gemini Omni Interactions (web-session transport)", Description: "Official Interactions request/response shape over the Gemini web video tool; inline text-to-video and stored follow-up turns only", SupportedGenerationMethods: []string{"interactions"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"video"}})
	}
	return models
}
