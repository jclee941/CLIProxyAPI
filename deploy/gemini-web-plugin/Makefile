GO ?= go
CC ?= cc
ARTIFACT := gemini-web.so

.PHONY: test build smoke check
test:
	$(GO) test -race -shuffle=on -count=1 ./...

build:
	CGO_ENABLED=1 $(GO) build -trimpath -buildmode=c-shared -o $(ARTIFACT) .

smoke: build
	$(CC) -Wall -Wextra -Werror -o abi-smoke smoke/load.c smoke/interceptor.c -ldl
	./abi-smoke ./$(ARTIFACT)

check: test
	$(GO) vet ./...
	$(MAKE) smoke
