package main

import "sync/atomic"

var slotCursor atomic.Uint64

// pickServableAccount answers the host scheduler with an account whose session
// can actually take work. Round-robin alone keeps handing turns to an account
// whose session is pinned or fenced, and every one of those fails in
// milliseconds, so the fleet looks intermittent while healthy accounts idle.
func (service *service) pickServableAccount(candidates []struct{ ID, Provider string }) continuationPick {
	store := service.localStore()
	if store == nil || len(candidates) == 0 {
		return continuationPick{}
	}
	records, err := store.records()
	if err != nil {
		return continuationPick{}
	}
	servable := make(map[string]bool, len(records))
	for _, local := range records {
		if local.State != localReady {
			continue
		}
		state := service.leases.get(local.Target.TokenRef).snapshot()
		if state.state == maintenanceOperator || state.state == maintenanceFenced && service.now().Before(state.nextDue) {
			continue
		}
		servable[local.Target.ID] = true
	}
	start := int(slotCursor.Add(1) % uint64(len(candidates)))
	var fallback string
	for offset := range candidates {
		candidate := candidates[(start+offset)%len(candidates)]
		if candidate.Provider != provider || !servable[candidate.ID] {
			continue
		}
		if fallback == "" {
			fallback = candidate.ID
		}
		if service.slotIsIdle(candidate.ID) {
			return continuationPick{AuthID: candidate.ID, Handled: true}
		}
	}
	if fallback == "" {
		return continuationPick{}
	}
	return continuationPick{AuthID: fallback, Handled: true}
}

// slotIsIdle probes the exclusive guard a generation takes. It is a scheduling
// hint, not a reservation: the executor still acquires the lease itself.
func (service *service) slotIsIdle(id string) bool {
	service.leases.mu.Lock()
	lease := service.leases.refs["account:"+id]
	service.leases.mu.Unlock()
	if lease == nil {
		return true
	}
	if !lease.guard.TryLock() {
		return false
	}
	lease.guard.Unlock()
	return true
}
