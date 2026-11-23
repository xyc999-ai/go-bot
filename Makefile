HOST_IMAGE ?= ghcr.io/xyc999-ai/go-bot-host:latest
CONTROLLER_IMAGE ?= ghcr.io/xyc999-ai/go-bot-controller:latest
.PHONY: build build-web image image-controller test race vet run check
build:
	mkdir -p bin
	go build -trimpath -o bin/controller ./cmd/controller
	go build -trimpath -o bin/host ./cmd/host
	go build -trimpath -o bin/buildeci ./cmd/buildeci
build-web:
	npm --prefix frontend ci --prefer-offline
	npm --prefix frontend run build
	mkdir -p bin
	go build -tags webui -trimpath -o bin/controller ./cmd/controller
image:
	docker build -f build/Dockerfile.host -t $(HOST_IMAGE) .
image-controller:
	docker build -f build/Dockerfile.controller -t $(CONTROLLER_IMAGE) .
test:
	go test ./...
race:
	go test -race ./harness/agent ./harness/session ./harness/schedule ./internal/hostruntime ./internal/controller ./internal/config ./internal/plugins ./internal/mcpclient ./internal/credentials ./internal/authorization
vet:
	go vet ./...
run:
	./scripts/start.sh
check:
	./scripts/check-dependencies.sh
