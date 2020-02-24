FROM golang:1.13-buster AS build

WORKDIR /go/src/github.com/replicatedhq/ekco
COPY Makefile Makefile
COPY LICENSE LICENSE
COPY go.mod go.mod
COPY go.sum go.sum
COPY cmd cmd
COPY pkg pkg

RUN make build

FROM debian:buster-slim

COPY --from=build /go/src/github.com/replicatedhq/ekco/bin/* /usr/bin/

ARG commit=unknown
ENV COMMIT=$commit

CMD [ /usr/bin/ekco ]
