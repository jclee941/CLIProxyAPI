package main

import "sync"

type sessionLifecycle struct {
	mu        sync.Mutex
	active    int
	closing   bool
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
