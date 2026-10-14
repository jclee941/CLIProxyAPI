package pluginhost

import (
	"encoding/json"
	"fmt"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// keepDisabledOnSave enforces that a plugin save cannot re-enable a credential.
// It rejects a non-boolean disabled field before anything is persisted, keeps a
// saved auth disabled when the host already holds it as disabled, and returns
// the bytes to write: a single-credential file keeps "disabled": true so a
// reload stays disabled, while the other fields keep their exact JSON values.
func (h *Host) keepDisabledOnSave(data []byte, auths []*coreauth.Auth) ([]byte, error) {
	metadata := make(map[string]any)
	if errUnmarshal := json.Unmarshal(data, &metadata); errUnmarshal != nil {
		return nil, fmt.Errorf("invalid auth file: %w", errUnmarshal)
	}
	coreauth.NormalizeCredentialMetadata(metadata)
	if rawDisabled, exists := metadata["disabled"]; exists {
		if _, ok := rawDisabled.(bool); !ok {
			return nil, fmt.Errorf("invalid auth disabled: must be a boolean")
		}
	}
	if manager := h.currentAuthManager(); manager != nil {
		for _, auth := range auths {
			if auth == nil {
				continue
			}
			if existing, ok := manager.GetByID(auth.ID); !ok || existing == nil || !existing.Disabled {
				continue
			}
			auth.Disabled = true
			auth.Status = coreauth.StatusDisabled
			if auth.Metadata == nil {
				auth.Metadata = make(map[string]any)
			}
			auth.Metadata["disabled"] = true
		}
	}
	if len(auths) != 1 || auths[0] == nil || !auths[0].Disabled {
		return data, nil
	}
	var fields map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(data, &fields); errUnmarshal != nil {
		return nil, fmt.Errorf("invalid auth file: %w", errUnmarshal)
	}
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	fields["disabled"] = json.RawMessage("true")
	fileData, errMarshal := json.Marshal(fields)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal auth file: %w", errMarshal)
	}
	return fileData, nil
}
