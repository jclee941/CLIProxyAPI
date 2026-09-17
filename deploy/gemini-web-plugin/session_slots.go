package main

import "sync/atomic"

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
	start := int(slotCursor.Add(1) % uint64(len(candidates)))
	busy, chosen, best := "", "", -1.0
	for offset := range candidates {
		candidate := candidates[(start+offset)%len(candidates)]
		reference, ok := servable[candidate.ID]
		if !ok || candidate.Provider != provider {
			continue
		}
		// An account whose five hour window is spent answers every turn with a
		// quota error, so it is skipped until the window resets rather than kept
		// in the rotation for a request that cannot succeed.
		headroom, usable := service.quotaHeadroom(candidate.ID)
		if !usable {
			continue
		}
		if !service.slotIsIdle(reference) {
			if busy == "" {
				busy = candidate.ID
			}
			continue
		}
		if chosen == "" {
			chosen, best = candidate.ID, headroom
			continue
		}
		// Rotation decides between accounts that are comparably fresh; headroom
		// only overrides it when one is materially more used than another, so a
		// newly linked account does not absorb the whole fleet's traffic.
		if headroom-best > quotaDivergence {
			chosen, best = candidate.ID, headroom
		}
	}
	if chosen != "" {
		return continuationPick{AuthID: chosen, Handled: true}
	}
	// Every account being mid-generation is a queue, not an outage. Returning
	// nothing here made the host answer "no credential", which reads as an
	// account problem and sends the operator to re-link six healthy accounts;
	// handing the busy one back lets the turn wait for the slot it needs.
	if busy == "" {
		return continuationPick{}
	}
	return continuationPick{AuthID: busy, Handled: true}
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
