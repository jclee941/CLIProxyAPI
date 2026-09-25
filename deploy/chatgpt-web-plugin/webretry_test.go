package main

import (
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestOnlyUnsentFailuresAreRetried(t *testing.T) {
	retryable := []error{
		&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
		&net.DNSError{Err: "no such host", Name: "chatgpt.com"},
	}
	for _, err := range retryable {
		if !webUnsentFailure(err) {
			t.Fatalf("a request that never reached the server was not retried: %v", err)
		}
	}

	kept := []error{
		&net.OpError{Op: "read", Err: syscall.ECONNRESET},
		&net.OpError{Op: "write", Err: syscall.EPIPE},
		io.ErrUnexpectedEOF,
		errors.New("context deadline exceeded"),
	}
	for _, err := range kept {
		if webUnsentFailure(err) {
			t.Fatalf("a failure that may have started a generation was retried: %v", err)
		}
	}
}

func TestRetryDelayBacksOffWithinBounds(t *testing.T) {
	for attempt := 0; attempt < webSendAttempts-1; attempt++ {
		floor := webRetryBase << attempt
		delay := webRetryDelay(attempt)
		if delay < floor || delay >= floor+webRetryJitterMax {
			t.Fatalf("attempt %d delay %v outside [%v, %v)", attempt, delay, floor, floor+webRetryJitterMax)
		}
	}
}

func TestPollBudgetStaysUnderTheCallerPatience(t *testing.T) {
	walk := time.Duration(webChatMaxCredentials) * (webInitialWait + webPollBudget)
	if walk > 300*time.Second {
		t.Fatalf("a full credential walk takes %v, long enough for callers to disconnect first", walk)
	}
}
