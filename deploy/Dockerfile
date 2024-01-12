FROM golang:1.21-bookworm AS build

ARG git_sha=unknown
ARG version=alpha
ARG rook_version=1.11.8

WORKDIR /go/src/github.com/replicatedhq/ekco
COPY go.mod go.sum ./
RUN go mod download
COPY Makefile Makefile
COPY LICENSE LICENSE
COPY cmd cmd
COPY pkg pkg

RUN curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
RUN helm repo add rook-release https://charts.rook.io/release
RUN helm repo update
RUN helm pull rook-release/rook-ceph-cluster --version $rook_version && mv rook-ceph-cluster-* pkg/helm/charts

RUN make build GIT_SHA=$git_sha VERSION=$version


FROM debian:bookworm-slim

RUN DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get upgrade -qq && apt-get install -y --no-install-recommends \
    libgcrypt20 \
    libgnutls30 \
    libhogweed4 \
    liblz4-1 \
    libnettle6 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /go/src/github.com/replicatedhq/ekco/bin/* /usr/bin/

ARG git_sha=unknown
ARG version=alpha

ENV GIT_SHA=$git_sha
ENV VERSION=$version

ENTRYPOINT /usr/bin/ekco
