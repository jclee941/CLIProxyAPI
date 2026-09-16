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
	// A generation holds the lease exclusively while a text turn shares it, so a
	// busy account is still servable for text and must not be handed back to a
	// scheduler that knows nothing about sessions.
	shared := model != omniModel && model != interactionOmniModel
	start := int(slotCursor.Add(1) % uint64(len(candidates)))
	busy := ""
	for offset := range candidates {
		candidate := candidates[(start+offset)%len(candidates)]
		reference, ok := servable[candidate.ID]
		if !ok || candidate.Provider != provider {
			continue
		}
		if service.slotIsIdle(reference) {
			return continuationPick{AuthID: candidate.ID, Handled: true}
		}
		if busy == "" {
			busy = candidate.ID
		}
	}
	if busy == "" || !shared {
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
