GO ?= go
CC ?= cc

.PHONY: test build smoke check
test:
	$(GO) test -race -shuffle=on -count=1 ./...
build:
	CGO_ENABLED=1 $(GO) build -trimpath -buildmode=c-shared -o flow2api.so .
smoke: build
	$(CC) -Wall -Wextra -Werror -o abi-smoke smoke/load.c -ldl
	./abi-smoke ./flow2api.so
check: test
	$(GO) vet ./...
	$(MAKE) smoke
