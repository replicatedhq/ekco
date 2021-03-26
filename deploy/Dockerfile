FROM golang:1.16-buster AS build

ARG git_sha=unknown
ARG version=alpha

WORKDIR /go/src/github.com/replicatedhq/ekco
COPY Makefile Makefile
COPY LICENSE LICENSE
COPY go.mod go.mod
COPY go.sum go.sum
COPY cmd cmd
COPY pkg pkg

RUN make build GIT_SHA=$git_sha VERSION=$version


FROM debian:buster-slim

RUN DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get install -y --no-install-recommends \
    libzstd1 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /go/src/github.com/replicatedhq/ekco/bin/* /usr/bin/

ARG git_sha=unknown
ARG version=alpha

ENV GIT_SHA=$git_sha
ENV VERSION=$version

ENTRYPOINT /usr/bin/ekco
