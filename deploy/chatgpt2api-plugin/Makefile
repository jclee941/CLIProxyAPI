GO ?= go
CC ?= cc
ARTIFACT := chatgpt2api.so

.PHONY: test build smoke check
build:
	CGO_ENABLED=1 $(GO) build -buildvcs=false -trimpath -buildmode=c-shared -o $(ARTIFACT) .

abi-smoke: smoke/c/load.c bridge.h
	$(CC) -Wall -Wextra -Werror -o $@ smoke/c/load.c -ldl

test: build abi-smoke
	$(GO) test -race -shuffle=on -count=1 ./...
	$(GO) test -race -tags=abismoke -shuffle=on -count=1 .

smoke: build abi-smoke
	$(GO) test -race -count=1 -v ./smoke
	$(GO) test -race -tags=abismoke -run TestCABI -count=1 -v .

check: test
	$(GO) vet ./...
	$(GO) vet -tags=abismoke .
