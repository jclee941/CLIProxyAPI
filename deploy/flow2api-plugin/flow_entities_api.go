package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Flow entities are the project's characters. The only entity kind Flow's web
// client creates is the character (entity_info type 1).
const flowEntityKindCharacter = 1

const flowEntityNotesLimit = 10000

type flowEntityImage struct {
	Slot       int    `json:"slot"`
	WorkflowID string `json:"workflowId"`
}

type flowEntityResource struct {
	ID               string            `json:"id"`
	ProjectID        string            `json:"projectId"`
	CollectionID     string            `json:"collectionId,omitempty"`
	Name             string            `json:"name"`
	Kind             string            `json:"kind"`
	PersonalityNotes string            `json:"personalityNotes"`
	Images           []flowEntityImage `json:"images"`
	VoiceIDs         []string          `json:"voiceIds"`
	PrimaryMediaID   string            `json:"primaryMediaId,omitempty"`
	Width            int               `json:"width,omitempty"`
	Height           int               `json:"height,omitempty"`
	Archived         bool              `json:"archived"`
	Favorited        bool              `json:"favorited"`
	CreatedAt        string            `json:"createdAt,omitempty"`
	UpdatedAt        string            `json:"updatedAt,omitempty"`
}

type flowEntityList struct {
	Entities []flowEntityResource `json:"entities"`
}

type flowEntityCreate struct {
	Name         string `json:"name"`
	CollectionID string `json:"collectionId"`
}

type flowEntityUpdate struct {
	Name             *string   `json:"name"`
	PersonalityNotes *string   `json:"personalityNotes"`
	VoiceIDs         *[]string `json:"voiceIds"`
	Favorited        *bool     `json:"favorited"`
	Archived         *bool     `json:"archived"`
	CollectionID     *string   `json:"collectionId"`
}

type flowEntityImageCopy struct {
	MediaID string `json:"mediaId"`
	Slot    *int   `json:"slot"`
}

// decodeFlowEntity reads [project, id, collection, info, primaryMedia,
// [width, height], created, updated] with info =
// [kind, name, [imageRefs, audioRefs, notes], favorited, archived].
func decodeFlowEntity(value any, project string) (flowEntityResource, error) {
	var result flowEntityResource
	result.ID, _ = jsonField(value, 1).(string)
	if projectID, _ := jsonField(value, 0).(string); projectID != project || !flowIdentifier(result.ID) {
		return result, failure(502, "flow_entity_identity_mismatch")
	}
	if kind, _ := jsonInteger(jsonField(value, 3, 0)); kind != flowEntityKindCharacter {
		return result, failure(502, "flow_entity_kind_unsupported")
	}
	result.ProjectID = project
	result.Kind = "character"
	result.CollectionID, _ = jsonField(value, 2).(string)
	result.Name, _ = jsonField(value, 3, 1).(string)
	result.PersonalityNotes, _ = jsonField(value, 3, 2, 2).(string)
	result.Favorited = flowFlag(jsonField(value, 3, 3))
	result.Archived = flowFlag(jsonField(value, 3, 4))
	result.PrimaryMediaID, _ = jsonField(value, 4).(string)
	result.Width, _ = jsonInteger(jsonField(value, 5, 0))
	result.Height, _ = jsonInteger(jsonField(value, 5, 1))
	result.CreatedAt = flowTimestamp(jsonField(value, 6))
	result.UpdatedAt = flowTimestamp(jsonField(value, 7))
	result.Images = []flowEntityImage{}
	references, _ := jsonField(value, 3, 2, 0).([]any)
	for slot, reference := range references {
		if workflow, _ := jsonField(reference, 0).(string); workflow != "" {
			result.Images = append(result.Images, flowEntityImage{Slot: slot, WorkflowID: workflow})
		}
	}
	result.VoiceIDs = []string{}
	voices, _ := jsonField(value, 3, 2, 1).([]any)
	for _, voice := range voices {
		if id, _ := jsonField(voice, 0).(string); id != "" {
			result.VoiceIDs = append(result.VoiceIDs, id)
		}
	}
	return result, nil
}

func flowEntities(contents any, project string) ([]flowEntityResource, error) {
	rows := flowContentRows(contents, 5)
	result := make([]flowEntityResource, 0, len(rows))
	for _, row := range rows {
		if kind, _ := jsonInteger(jsonField(row, 3, 0)); kind != flowEntityKindCharacter {
			continue
		}
		entity, err := decodeFlowEntity(row, project)
		if err != nil {
			return nil, err
		}
		result = append(result, entity)
	}
	return result, nil
}

func flowFindEntity(entities []flowEntityResource, id string) (flowEntityResource, bool) {
	for _, entity := range entities {
		if entity.ID == id {
			return entity, true
		}
	}
	return flowEntityResource{}, false
}

func flowEntityNotes(notes string) error {
	if !utf8.ValidString(notes) || utf8.RuneCountInString(notes) > flowEntityNotesLimit ||
		strings.ContainsFunc(notes, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) {
		return failure(400, "flow_entity_notes_invalid")
	}
	return nil
}

func flowEntityResult(payload any, project string) (flowEntityResource, error) {
	return decodeFlowEntity(jsonField(payload, 0), project)
}

func (service *service) flowEntityHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" {
		switch request.Method {
		case http.MethodGet:
			return service.listFlowEntities(ctx, record, project, request)
		case http.MethodPost:
			return service.createFlowEntity(ctx, record, project, request.Body)
		}
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	if id, rest, nested := strings.Cut(tail, "/"); nested {
		return service.flowEntityImageHTTP(ctx, record, project, id, rest, request)
	}
	id, action, ok := flowSplitTail(tail)
	if !ok {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	entities, err := flowEntities(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	current, found := flowFindEntity(entities, id)
	if !found {
		return httpResponse{}, failure(404, "flow_entity_not_found")
	}
	archive := func(archived bool) (flowEntityResource, error) {
		return service.updateFlowEntity(ctx, record, project, current, contents, flowEntityUpdate{Archived: &archived})
	}
	switch {
	case request.Method == http.MethodGet && action == "":
		return flowJSON(http.StatusOK, current)
	case request.Method == http.MethodPatch && action == "":
		var input flowEntityUpdate
		if err := flowStrict("entity", request.Body, &input); err != nil {
			return httpResponse{}, err
		}
		updated, err := service.updateFlowEntity(ctx, record, project, current, contents, input)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, updated)
	case request.Method == http.MethodDelete && action == "":
		if _, err := archive(true); err != nil {
			return httpResponse{}, err
		}
		return flowNoContent(), nil
	case request.Method == http.MethodPost && action == "restore":
		restored, err := archive(false)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, restored)
	case request.Method == http.MethodPost && action == "copy":
		return service.copyFlowEntity(ctx, record, project, current, request.Body)
	case request.Method == http.MethodPost && action == "purge":
		if !current.Archived {
			return httpResponse{}, failure(409, "flow_entity_not_archived")
		}
		// BatchDeleteAssets carries character ids in field 4.
		if _, err := service.flowProjectRPC(ctx, record, project, "cz8Z4b", flowWireRow(nil, nil, project, []string{current.ID})); err != nil {
			return httpResponse{}, err
		}
		return flowNoContent(), nil
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) listFlowEntities(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	archived, err := flowArchivedFilter(request.Query)
	if err != nil {
		return httpResponse{}, err
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	entities, err := flowEntities(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	collections, filterCollection := request.Query["collectionId"]
	result := flowEntityList{Entities: []flowEntityResource{}}
	for _, entity := range entities {
		if entity.Archived != archived || !flowTitleMatches(entity.Name, request.Query.Get("search")) ||
			filterCollection && (len(collections) == 0 || entity.CollectionID != collections[0]) {
			continue
		}
		result.Entities = append(result.Entities, entity)
	}
	return flowJSON(http.StatusOK, result)
}

func (service *service) createFlowEntity(ctx context.Context, record storageRecord, project string, body []byte) (httpResponse, error) {
	var input flowEntityCreate
	if err := flowStrict("entity", body, &input); err != nil {
		return httpResponse{}, err
	}
	name, err := flowResourceTitle(input.Name)
	if err != nil {
		return httpResponse{}, err
	}
	var collection any
	if input.CollectionID != "" {
		contents, err := service.flowProjectContents(ctx, record, project)
		if err != nil {
			return httpResponse{}, err
		}
		if collection, err = flowDestinationCollection(contents, project, input.CollectionID); err != nil {
			return httpResponse{}, err
		}
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "C4BZMd", []any{
		[]any{project, nil, collection, []any{flowEntityKindCharacter, name, []any{}}},
	})
	if err != nil {
		return httpResponse{}, err
	}
	created, err := flowEntityResult(payload, project)
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusCreated, created)
}

// updateFlowEntity sends only the masked fields in one UpdateEntity call, so
// every unmasked field keeps its stored value and a failure leaves nothing
// half applied.
func (service *service) updateFlowEntity(ctx context.Context, record storageRecord, project string, current flowEntityResource, contents any, input flowEntityUpdate) (flowEntityResource, error) {
	info := make([]any, 5)
	info[0] = flowEntityKindCharacter
	character := make([]any, 3)
	var paths []string
	var collection any
	if input.Name != nil {
		name, err := flowResourceTitle(*input.Name)
		if err != nil {
			return current, err
		}
		info[1] = name
		paths = append(paths, "entity_info.display_name")
	}
	if input.PersonalityNotes != nil {
		if err := flowEntityNotes(*input.PersonalityNotes); err != nil {
			return current, err
		}
		character[2] = *input.PersonalityNotes
		paths = append(paths, "entity_info.character_info.personality_notes")
	}
	if input.VoiceIDs != nil {
		if err := flowUniqueIdentifiers(*input.VoiceIDs, flowResourceBatchLimit); err != nil {
			return current, err
		}
		references := make([]any, len(*input.VoiceIDs))
		for index, id := range *input.VoiceIDs {
			references[index] = []any{id}
		}
		character[1] = references
		paths = append(paths, "entity_info.character_info.audio_references")
	}
	if input.Favorited != nil {
		info[3] = *input.Favorited
		paths = append(paths, "entity_info.is_favorited")
	}
	if input.Archived != nil {
		info[4] = *input.Archived
		paths = append(paths, "entity_info.archived")
	}
	if input.CollectionID != nil {
		if _, err := flowDestinationCollection(contents, project, *input.CollectionID); err != nil {
			return current, err
		}
		collection = *input.CollectionID
		paths = append(paths, "collection_id")
	}
	if len(paths) == 0 {
		return current, failure(400, "flow_entity_request_invalid")
	}
	if row := flowWireRow(character...); row != nil {
		info[2] = row
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "rzMKMb", []any{
		flowWireRow(project, current.ID, collection, flowWireRow(info...)), flowMask(paths...),
	})
	if err != nil {
		return current, err
	}
	updated, err := flowEntityResult(payload, project)
	if err == nil && updated.ID != current.ID {
		err = failure(502, "flow_entity_identity_mismatch")
	}
	return updated, err
}

// CopyEntity takes only the project and the entity, so a copy stays in the
// project that owns the source.
func (service *service) copyFlowEntity(ctx context.Context, record storageRecord, project string, current flowEntityResource, body []byte) (httpResponse, error) {
	if len(body) > 0 {
		var none struct{}
		if err := flowStrict("entity_copy", body, &none); err != nil {
			return httpResponse{}, err
		}
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "YIBQMe", []any{project, current.ID})
	if err != nil {
		return httpResponse{}, err
	}
	copied, err := flowEntityResult(payload, project)
	if err != nil {
		return httpResponse{}, err
	}
	if copied.ID == current.ID {
		return httpResponse{}, failure(502, "flow_entity_identity_mismatch")
	}
	return flowJSON(http.StatusCreated, copied)
}

func (service *service) flowEntityImageHTTP(ctx context.Context, record storageRecord, project, id, rest string, request flowHTTPRequest) (httpResponse, error) {
	if !flowIdentifier(id) || strings.Contains(id, ":") {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	slotText, positional := strings.CutPrefix(rest, "images/")
	slot, err := strconv.Atoi(slotText)
	if rest != "images" && (!positional || err != nil || slot < 0) {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	add := rest == "images" && request.Method == http.MethodPost
	remove := positional && request.Method == http.MethodDelete
	if !add && !remove {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	entities, err := flowEntities(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	current, found := flowFindEntity(entities, id)
	if !found {
		return httpResponse{}, failure(404, "flow_entity_not_found")
	}
	if add {
		return service.copyFlowEntityImage(ctx, record, project, current, contents, request.Body)
	}
	if !flowEntityHasImage(current, slot) {
		return httpResponse{}, failure(404, "flow_entity_image_not_found")
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "LXpojc", []any{nil, project, current.ID, slot})
	if err != nil {
		return httpResponse{}, err
	}
	updated, err := flowEntityResult(payload, project)
	if err == nil && updated.ID != current.ID {
		err = failure(502, "flow_entity_identity_mismatch")
	}
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, updated)
}

func flowEntityHasImage(entity flowEntityResource, slot int) bool {
	for _, image := range entity.Images {
		if image.Slot == slot {
			return true
		}
	}
	return false
}

// copyFlowEntityImage runs CopyProjectMedia with the character slot as its
// destination, as the web client does for "use image as reference". The new
// reference is read back from the project because the call answers with the
// copied workflow, not the entity. The mutation is sent once; a failed
// read-back is reported without resending it.
func (service *service) copyFlowEntityImage(ctx context.Context, record storageRecord, project string, current flowEntityResource, contents any, body []byte) (httpResponse, error) {
	var input flowEntityImageCopy
	if err := flowStrict("entity_image", body, &input); err != nil {
		return httpResponse{}, err
	}
	if !flowIdentifier(input.MediaID) || strings.ContainsAny(input.MediaID, "/:") || input.Slot == nil {
		return httpResponse{}, failure(400, "flow_entity_request_invalid")
	}
	if *input.Slot < 0 || int64(*input.Slot) > 1<<31-1 {
		return httpResponse{}, failure(400, "flow_entity_slot_invalid")
	}
	var image *flowMediaResource
	for _, row := range flowContentRows(contents, 2) {
		media, err := decodeFlowMedia(row, project)
		if err != nil {
			return httpResponse{}, err
		}
		if media.ID == input.MediaID {
			image = &media
		}
	}
	if image == nil {
		return httpResponse{}, failure(404, "flow_media_not_found")
	}
	if image.Type != "image" {
		return httpResponse{}, failure(400, "flow_entity_reference_requires_image")
	}
	reference := []any{nil, nil, []any{current.ID, []any{*input.Slot}}}
	if _, err := service.flowProjectRPC(ctx, record, project, "Sc7aEb", flowWireRow(
		image.ID, nil, nil, project, nil, nil, nil, reference, nil, flowID(), flowID(),
	)); err != nil {
		return httpResponse{}, err
	}
	fresh, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, failure(502, "flow_entity_refresh_failed")
	}
	entities, err := flowEntities(fresh, project)
	if err != nil {
		return httpResponse{}, failure(502, "flow_entity_refresh_failed")
	}
	updated, found := flowFindEntity(entities, current.ID)
	if !found {
		return httpResponse{}, failure(502, "flow_entity_refresh_failed")
	}
	return flowJSON(http.StatusCreated, updated)
}
