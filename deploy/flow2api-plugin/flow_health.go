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

// flowHealthEvery is how often an account is checked when Flow accepted
// nothing from it in that time; health_check_interval_seconds replaces it.
const flowHealthEvery = time.Hour

// flowHealthTick is how often the accounts are looked at for a periodic check.
const flowHealthTick = time.Minute

// flowHealthRetry is the wait before a held account's check that could not
// tell, for example because the project lookup failed, runs again.
const flowHealthRetry = 5 * time.Minute

func (service *service) healthCheckRetry() time.Duration {
	if seconds := service.settings().HealthCheckRetrySeconds; seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return flowHealthRetry
}

// healthCheckInterval is how often an idle account is checked, or zero when
// periodic checks are off.
func (service *service) healthCheckInterval() time.Duration {
	switch seconds := service.settings().HealthCheckIntervalSeconds; {
	case seconds < 0:
		return 0
	case seconds == 0:
		return flowHealthEvery
	default:
		return time.Duration(seconds) * time.Second
	}
}

// runPeriodicHealthChecks looks at the accounts every flowHealthTick until the
// host shuts the plugin down.
func (service *service) runPeriodicHealthChecks() {
	for {
		timer := time.NewTimer(flowHealthTick)
		select {
		case <-service.lifetime.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		service.periodicHealthChecks()
	}
}

// periodicHealthChecks checks each configured account that is not held, has
// no check scheduled, and had neither an accepted request nor a check within
// the interval, so an account callers are using is never checked.
func (service *service) periodicHealthChecks() {
	every := service.healthCheckInterval()
	if every == 0 {
		return
	}
	for _, account := range service.settings().FlowAccounts {
		if service.lifetime.Err() != nil {
			return
		}
		now := service.now()
		service.flowMu.Lock()
		due := service.holds[account] == nil && now.Sub(service.flowAccepted[account]) >= every && now.Sub(service.healthChecked[account]) >= every
		if due {
			if service.healthChecked == nil {
				service.healthChecked = map[string]time.Time{}
			}
			service.healthChecked[account] = now
		}
		service.flowMu.Unlock()
		if due {
			_ = service.runHealthCheck(storageRecord{ID: account, SourceAuthID: account}, "periodic") // The outcome is recorded and reported.
		}
	}
}

// holdCheck runs the check a hold scheduled, unless a newer hold replaced it.
// An accepted image ends the hold and a refusal reschedules it, both through
// noteUnusualActivity; a check that could not tell keeps the hold and runs
// again after healthCheckRetry, unless the hold changed meanwhile.
func (service *service) holdCheck(record storageRecord, generation uint64) {
	account := record.SourceAuthID
	service.flowMu.Lock()
	hold := service.holds[account]
	current := hold != nil && hold.generation == generation
	if current {
		hold.until, hold.timer = service.now().Add(flowHealthWindow), nil
	}
	service.flowMu.Unlock()
	if !current {
		return
	}
	err := service.runHealthCheck(record, "hold")
	if err == nil || flowUnusualActivity(err) {
		return
	}
	retry := service.healthCheckRetry()
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if hold := service.holds[account]; hold != nil && hold.generation == generation {
		service.scheduleCheckLocked(record, hold, retry)
	}
}

// runHealthCheck generates one image for the account in the health check
// project and records the outcome; a failure other than an unusual-activity
// refusal marks the account failing.
func (service *service) runHealthCheck(record storageRecord, kind string) error {
	account := record.SourceAuthID
	err := service.flowHealthGenerate(service.lifetime, record)
	state := "flow_health_check_passed"
	if err != nil {
		state = safeCredentialMessage(err)
	}
	service.noteHealthCheck(account, kind, err)
	if err != nil && !flowUnusualActivity(err) && service.lifetime.Err() == nil {
		service.markHealth(account, flowHealthFailing, safeCredentialCode(err), time.Time{})
	}
	service.report(map[string]any{"provider": provider, "state": state, "reason": flowHealthModel + " " + kind}, "flow2api: health check")
	return err
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
