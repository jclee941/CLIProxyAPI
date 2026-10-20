package main

import (
	"context"
	"encoding/json"
	"time"
)

// flowHealthTitle names the project a health check generates in, kept apart
// from the project callers' media is filed under.
const flowHealthTitle = "CLIProxyAPI health check"

// A health check generates one Nano Banana 2 image, which costs no credits.
const (
	flowHealthModel  = "flow-nano-banana-2"
	flowHealthPrompt = "health check: a red apple on a wooden table"
)

// flowHealthWindow keeps the account held while its health check runs, so no
// caller's request probes Flow alongside it.
const flowHealthWindow = 10 * time.Minute

// flowHealthPage is the page size the health check project is looked up with.
const flowHealthPage = 100

// scheduleHealthCheck checks the held account when its hold ends.
func (service *service) scheduleHealthCheck(record storageRecord, hold time.Duration) {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.lifetime.Err() != nil {
		return
	}
	if service.healthChecks == nil {
		service.healthChecks = map[string]*time.Timer{}
	}
	if pending := service.healthChecks[record.SourceAuthID]; pending != nil {
		pending.Stop()
	}
	service.healthChecks[record.SourceAuthID] = service.afterFunc(hold, func() { service.flowHealthCheck(record) })
}

// flowHealthCheck generates one image for the held account in the health
// check project. Flow accepting it, or answering anything but unusual
// activity, ends the hold. Another unusual-activity refusal doubles the hold,
// and flowGenerate schedules the next check.
func (service *service) flowHealthCheck(record storageRecord) {
	account := record.SourceAuthID
	service.flowMu.Lock()
	delete(service.healthChecks, account)
	service.unusualHeldUntil[account] = service.now().Add(flowHealthWindow)
	service.flowMu.Unlock()
	err := service.flowHealthGenerate(service.lifetime, record)
	state := "flow_health_check_passed"
	if err != nil {
		state = safeCredentialMessage(err)
	}
	if !flowUnusualActivity(err) {
		service.flowMu.Lock()
		delete(service.unusualHeldUntil, account)
		service.flowMu.Unlock()
	}
	service.report(map[string]any{"provider": provider, "state": state, "reason": flowHealthModel}, "flow2api: health check")
}

func (service *service) flowHealthGenerate(ctx context.Context, record storageRecord) error {
	project, err := service.flowHealthProject(ctx, record)
	if err != nil {
		return err
	}
	model, ok := flowModelFor(flowHealthModel)
	if !ok {
		return failure(500, "flow_unsupported_model")
	}
	payload, err := json.Marshal(map[string]any{
		"contents":         []any{map[string]any{"parts": []any{map[string]any{"text": flowHealthPrompt}}}},
		"generationConfig": map[string]any{"flow": map[string]any{"projectId": project}},
	})
	if err != nil {
		return failure(500, "flow_request_encoding_failed")
	}
	input, err := parseFlowRequest(model, payload)
	if err != nil {
		return err
	}
	input.healthCheck = true
	_, err = service.flowGenerate(ctx, record, model, input)
	return err
}

// flowHealthProject is the configured health check project, else the
// account's project titled flowHealthTitle, created when it has none.
func (service *service) flowHealthProject(ctx context.Context, record storageRecord) (string, error) {
	if project := service.settings().HealthCheckProject; project != "" {
		return project, nil
	}
	service.flowMu.Lock()
	project := service.healthProjects[record.SourceAuthID]
	service.flowMu.Unlock()
	for token := ""; project == ""; {
		page, err := service.listFlowProjects(ctx, record, flowHealthPage, token)
		if err != nil {
			return "", err
		}
		for _, candidate := range page.Projects {
			if candidate.Title == flowHealthTitle {
				project = candidate.ID
				break
			}
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	if project == "" {
		created, err := service.createFlowProject(ctx, record, flowHealthTitle)
		if err != nil {
			return "", err
		}
		project = created.ID
	}
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.healthProjects == nil {
		service.healthProjects = map[string]string{}
	}
	service.healthProjects[record.SourceAuthID] = project
	return project, nil
}
