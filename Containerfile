FROM docker.io/library/golang:alpine AS build

COPY . /work
WORKDIR /work

RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod go build -v -o nimby ./cmd

FROM scratch
LABEL org.opencontainers.image.description "A really simple ingress proxy for [Nomad](https://developer.hashicorp.com/nomad)"

## Use the task API socket mounted in the container by default
ENV NOMAD_ADDR="unix:///secrets/api.sock"

COPY --from=build /work/nimby /nimby
ENTRYPOINT ["/nimby"]
