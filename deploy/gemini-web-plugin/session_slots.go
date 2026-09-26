package main

import (
	"slices"
	"sync/atomic"
)

var slotCursor atomic.Uint64

// pickServableAccount answers the host scheduler with an account whose session
// can actually take work. Round-robin alone keeps handing turns to an account
// whose session is pinned or fenced, and every one of those fails in
// milliseconds, so the fleet looks intermittent while healthy accounts idle.
func (service *service) pickServableAccount(model string, candidates []struct{ ID, Provider string }) continuationPick {
	store := service.localStore()
	if store == nil || len(candidates) == 0 {
		return continuationPick{}
	}
	records, err := store.records()
	if err != nil {
		return continuationPick{}
	}
	servable := make(map[string]string, len(records))
	for _, local := range records {
		if local.State != localReady {
			continue
		}
		state := service.leases.get(local.Target.TokenRef).snapshot()
		if state.state == maintenanceOperator || state.state == maintenanceFenced && service.now().Before(state.nextDue) {
			continue
		}
		servable[local.Target.ID] = local.Target.TokenRef
	}
	eligible := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := servable[candidate.ID]; !ok || candidate.Provider != provider {
			continue
		}
		if !service.quotaAvailable(candidate.ID) {
			continue
		}
		eligible = append(eligible, candidate.ID)
	}
	// A video room stays on the account that opens it, so it only opens where the
	// whole room is paid for; with no such account there is no room to open.
	if model == omniModel || model == interactionOmniModel {
		eligible = slices.DeleteFunc(eligible, func(id string) bool { return !service.affordsARoom(id) })
	}
	var idle, busy []string
	for _, id := range eligible {
		if !service.slotIsIdle(servable[id]) {
			busy = append(busy, id)
			continue
		}
		idle = append(idle, id)
	}
	if len(idle) == 0 {
		idle = busy
	}
	if len(idle) == 0 {
		return continuationPick{}
	}
	// Advance over eligible accounts only; rejected candidates must not skew
	// rotation, and host candidate ordering must not change the cycle.
	slices.Sort(idle)
	index := (slotCursor.Add(1) - 1) % uint64(len(idle))
	return continuationPick{AuthID: idle[index], Handled: true}
}

// slotIsIdle probes the exclusive guard a generation takes. It is a scheduling
// hint, not a reservation: the executor still acquires the lease itself.
func (service *service) slotIsIdle(reference string) bool {
	lease := service.leases.get(reference)
	if !lease.guard.TryLock() {
		return false
	}
	lease.guard.Unlock()
	return true
}

// ownerServes reports whether the account holding a chain can take its next
// turn. A busy owner still serves, since the turn waits for its slot; one with
// spent windows or video allowance, or a session no process can run, is blocked.
func (service *service) ownerServes(local localSession) bool {
	if !service.quotaAvailable(local.Target.ID) {
		return false
	}
	state := service.leases.get(local.Target.TokenRef).snapshot()
	if state.state == maintenanceOperator || state.state == maintenanceFenced && service.now().Before(state.nextDue) {
		return false
	}
	return local.State == localReady || !service.slotIsIdle(local.Target.TokenRef)
}

// runningTurn reports the generation an account is currently on. A submitted
// turn locks the session, and the account listing used to render that lock as
// needs_operator, which is the same thing a stranded account shows.
func (service *service) runningTurn(record storageRecord) *accountActivity {
	if !localReferencePattern.MatchString(record.TokenRef) {
		return nil
	}
	store := service.localStore()
	if store == nil {
		return nil
	}
	local, err := store.read(record.TokenRef)
	if err != nil || local.State != localSubmitting || local.ContinuationActive == "" {
		return nil
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return nil
	}
	turn, found := turns[local.ContinuationActive]
	// A submitted turn only streams inside the process that started it. One that
	// predates this process cannot be running, however recent it looks, so it is
	// an interrupted turn for recovery rather than progress to report.
	if !found || turn.State != "submitting" || turn.StartedAt <= service.startedAt {
		return nil
	}
	model := turn.Model
	if model == omniModel {
		model = interactionOmniModel
	}
	return &accountActivity{Model: model, StartedAt: turn.StartedAt, Summary: turn.Summary}
}

// busySessionToken reads the credential of a session that is mid-turn. Only the
// read-only account inspection uses it: execution still goes through resolveLocal,
// which refuses a session that is not ready.
func (service *service) busySessionToken(record storageRecord) (sessionToken, error) {
	local, err := service.localRecord(record)
	if err != nil {
		return sessionToken{}, err
	}
	if local.State != localSubmitting || local.ContinuationActive == "" {
		return sessionToken{}, failure(409, "needs_operator")
	}
	return sessionToken{local.Token}, nil
}
