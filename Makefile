SHELL := /bin/bash -o pipefail

export GO111MODULE=on

VERSION_PACKAGE = github.com/replicatedhq/ekco/pkg/version
VERSION ?= `git describe --tags --dirty`
DATE = `date -u +"%Y-%m-%dT%H:%M:%SZ"`
CURRENT_USER=$(shell id -u -n)

ifndef GIT_SHA
GIT_TREE = $(shell git rev-parse --is-inside-work-tree 2>/dev/null)
ifneq "$(GIT_TREE)" ""
define GIT_UPDATE_INDEX_CMD
git update-index --assume-unchanged
endef
define GIT_SHA
`git rev-parse HEAD`
endef
else
define GIT_UPDATE_INDEX_CMD
echo "Not a git repo, skipping git update-index"
endef
define GIT_SHA
""
endef
endif
endif

define LDFLAGS
-ldflags "\
	-X $(VERSION_PACKAGE).version=$(VERSION) \
	-X $(VERSION_PACKAGE).gitSHA=$(GIT_SHA) \
	-X $(VERSION_PACKAGE).buildTime=$(DATE) \
"
endef

.PHONY: clean
clean:
	rm -rf ./bin

.PHONY: deps
deps:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: lint vet
	go test ./pkg/... ./cmd/...

.PHONY: build
build:
	go build $(LDFLAGS) -o bin/ekco cmd/ekco/main.go

DOCKER_IMAGE ?= replicated/ekco:latest

.PHONY: docker-image
docker-image:
	docker build \
		-t $(DOCKER_IMAGE) \
		-f deploy/Dockerfile \
		--build-arg git_sha="$(GIT_SHA)" \
		--build-arg version="$(VERSION)" \
		.
build-ttl.sh:
	docker build \
		-t ttl.sh/${CURRENT_USER}/ekco:12h \
		-f deploy/Dockerfile \
		--build-arg git_sha=dev \
		--build-arg version=dev \
		.
	docker push ttl.sh/${CURRENT_USER}/ekco:12h
