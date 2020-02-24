SHELL := /bin/bash

DOCKER_IMAGE ?= replicated/ekco:latest

export GO111MODULE=on

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

.PHONY: clean
clean:
	rm -rf ./bin

.PHONY: deps
deps:
	go get golang.org/x/lint/golint

.PHONY: lint
lint:
	# TODO: fix lint errors
	golint -set_exit_status ./... \
		| grep -v "should have comment" \
		| grep -v "comment on exported method" || :

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: lint vet
	go test ./pkg/... ./cmd/...

.PHONY: build
build:
	go build -o bin/ekco cmd/ekco/main.go

.PHONY: docker-image
docker-image:
	docker build -t $(DOCKER_IMAGE) -f deploy/Dockerfile --build-arg commit="${GIT_SHA}" .
