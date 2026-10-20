package main

import (
	"sync"
	"time"
)

type sessionLifecycle struct {
	mu        sync.Mutex
	active    int
	closing   bool
	quiesced  bool
	closingCh chan struct{}
	drained   chan struct{}
	closeErr  error
}

func (lifecycle *sessionLifecycle) enter() error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closing {
		if lifecycle.closeErr != nil {
			return failure(503, "session_store_shutdown_failed")
		}
		return failure(503, "plugin_shutdown")
	}
	lifecycle.active++
	return nil
}

func (lifecycle *sessionLifecycle) leave() {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.active--
	if lifecycle.closing && lifecycle.active == 0 {
		close(lifecycle.drained)
	}
}

func (lifecycle *sessionLifecycle) closingSignal() <-chan struct{} {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closingCh == nil {
		lifecycle.closingCh = make(chan struct{})
	}
	return lifecycle.closingCh
}

func (lifecycle *sessionLifecycle) reconfigure(apply func() error) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closing || lifecycle.active > 1 {
		return failure(409, "session_busy")
	}
	return apply()
}

func (service *service) shutdownSessions() error {
	service.lifecycle.mu.Lock()
	if !service.lifecycle.closing {
		service.lifecycle.closing = true
		service.lifecycle.drained = make(chan struct{})
		if service.lifecycle.closingCh == nil {
			service.lifecycle.closingCh = make(chan struct{})
		}
		close(service.lifecycle.closingCh)
		if service.lifecycle.active == 0 {
			close(service.lifecycle.drained)
		}
	}
	drained := service.lifecycle.drained
	service.lifecycle.mu.Unlock()
	<-drained
	service.client.CloseIdleConnections()
	var err error
	if store := service.localStore(); store != nil {
		err = store.close()
	}
	service.lifecycle.mu.Lock()
	service.lifecycle.closeErr = err
	service.lifecycle.mu.Unlock()
	return err
}

func (service *service) stop() {
	err := service.shutdownSessions()
	service.lifecycle.mu.Lock()
	service.lifecycle.closeErr = err
	service.lifecycle.mu.Unlock()
}

// quiesceDrainLimit bounds how long a hot reload waits for running calls before
// it gives the session store hand-over up. The deploy converger restarts the
// container when the replacement has not loaded within its health wait (180 s
// by default), so the drain has to end well inside that.
var quiesceDrainLimit = 90 * time.Second

// quiesce readies this instance for a replacement loaded into the same host,
// which calls it before loading the new build: it refuses new calls, stops the
// background loops, waits for running calls, and closes the session store so
// the replacement can take its owner lock. Calls that outlast the drain limit
// abandon the quiesce and leave the instance serving, because closing the
// store under a running call could drop a credential rotation; the replacement
// then cannot take the lock and the host keeps this instance.
func (service *service) quiesce() error {
	service.lifecycle.mu.Lock()
	if service.lifecycle.closing {
		service.lifecycle.mu.Unlock()
		return nil
	}
	service.lifecycle.closing, service.lifecycle.quiesced = true, true
	service.lifecycle.drained = make(chan struct{})
	if service.lifecycle.closingCh == nil {
		service.lifecycle.closingCh = make(chan struct{})
	}
	close(service.lifecycle.closingCh)
	if service.lifecycle.active == 0 {
		close(service.lifecycle.drained)
	}
	drained := service.lifecycle.drained
	service.lifecycle.mu.Unlock()
	limit := time.NewTimer(service.tuning().quiesceDrain())
	defer limit.Stop()
	select {
	case <-drained:
	case <-limit.C:
		select {
		case <-drained:
		default:
			service.abortQuiesce()
			return failure(409, "quiesce_drain_timeout")
		}
	}
	service.client.CloseIdleConnections()
	service.mu.Lock()
	store := service.sessions
	service.sessions = nil
	service.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.close()
}

// abortQuiesce returns an instance whose calls did not drain to serving and
// restarts the background loops the quiesce stopped.
func (service *service) abortQuiesce() {
	service.lifecycle.mu.Lock()
	service.lifecycle.closing, service.lifecycle.quiesced = false, false
	service.lifecycle.closingCh, service.lifecycle.drained = nil, nil
	service.lifecycle.mu.Unlock()
	if service.localStore() != nil {
		service.startKeepAlive()
		service.startTrappedSessionRelease()
	}
}

// resumeQuiesced readmits calls on a quiesced instance the host kept because
// its replacement failed to load; the host then registers it again, and that
// register reopens the session store and its loops.
func (service *service) resumeQuiesced() {
	service.lifecycle.mu.Lock()
	defer service.lifecycle.mu.Unlock()
	if !service.lifecycle.quiesced {
		return
	}
	service.lifecycle.closing, service.lifecycle.quiesced = false, false
	service.lifecycle.closingCh, service.lifecycle.drained, service.lifecycle.closeErr = nil, nil, nil
}
