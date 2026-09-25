package main

import (
	"errors"
	"math/rand"
	"net"
	"time"
)

const (
	webSendAttempts   = 3
	webRetryBase      = 600 * time.Millisecond
	webRetryJitterMax = 200 * time.Millisecond
)

// Only failures that provably sent nothing are retried. A reset raised after the
// connection is established may already have produced an image, so retrying it
// would spend a second one from a budget that runs out after about 25.
func webUnsentFailure(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func webRetryDelay(attempt int) time.Duration {
	return webRetryBase<<attempt + time.Duration(rand.Int63n(int64(webRetryJitterMax)))
}
