package main

const (
	flowModeAuto       = "auto"
	flowModeText       = "text"
	flowModeReferences = "references"
	flowModeFrames     = "frames"
	flowModeEdit       = "edit"
	flowModeExtend     = "extend"
	flowModeUpscale    = "upscale"
)

type flowVideoReference struct {
	MediaID    string
	StartFrame *int
	EndFrame   *int
}

type flowDestination struct {
	WorkflowID   string
	CollectionID string
	SceneID      string
	Position     *int
}

type flowPromptPart struct {
	Text       string
	MediaID    string
	LikenessID string
	EntityID   string
	AudioID    string
}

func (part flowPromptPart) isReference() bool {
	return part.MediaID != "" || part.LikenessID != "" || part.EntityID != "" || part.AudioID != ""
}

// flowOptions are the Flow-specific parameters under generationConfig.flow.
// Mode is always resolved (never "auto") and Priority defaults to "normal"
// once parseFlowRequest returns.
type flowOptions struct {
	Mode                   string
	Priority               string
	ProjectID              string
	ModelKey               string
	FirstFrame             *flowReference
	LastFrame              *flowReference
	BaseImage              *flowReference
	ReferenceImages        []flowReference
	SourceVideo            *flowVideoReference
	ReferenceAudio         []string
	ReferenceLikenesses    []string
	ReferenceEntities      []string
	AudioFailurePreference string
	StructuredPrompt       []flowPromptPart
	Destination            *flowDestination
}

type flowCropWire struct {
	Top    *float64 `json:"top"`
	Left   *float64 `json:"left"`
	Bottom *float64 `json:"bottom"`
	Right  *float64 `json:"right"`
}

type flowReferenceWire struct {
	MediaID    string `json:"mediaId"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	CropCoordinates *flowCropWire `json:"cropCoordinates"`
}

type flowOptionsWire struct {
	Mode            string              `json:"mode"`
	Priority        string              `json:"priority"`
	ProjectID       string              `json:"projectId"`
	ModelKey        string              `json:"modelKey"`
	FirstFrame      *flowReferenceWire  `json:"firstFrame"`
	LastFrame       *flowReferenceWire  `json:"lastFrame"`
	BaseImage       *flowReferenceWire  `json:"baseImage"`
	ReferenceImages []flowReferenceWire `json:"referenceImages"`
	SourceVideo     *struct {
		MediaID    string `json:"mediaId"`
		StartFrame *int   `json:"startFrame"`
		EndFrame   *int   `json:"endFrame"`
	} `json:"sourceVideo"`
	ReferenceAudio         []string `json:"referenceAudio"`
	ReferenceLikenesses    []string `json:"referenceLikenesses"`
	ReferenceEntities      []string `json:"referenceEntities"`
	AudioFailurePreference string   `json:"audioFailurePreference"`
	Destination            *struct {
		WorkflowID   string `json:"workflowId"`
		CollectionID string `json:"collectionId"`
		SceneID      string `json:"sceneId"`
		Position     *int   `json:"position"`
	} `json:"destination"`
	StructuredPrompt *struct {
		Parts []struct {
			Text      *string `json:"text"`
			Reference *struct {
				MediaID    string `json:"mediaId"`
				LikenessID string `json:"likenessId"`
				EntityID   string `json:"entityId"`
				AudioID    string `json:"audioId"`
			} `json:"reference"`
		} `json:"parts"`
	} `json:"structuredPrompt"`
}
