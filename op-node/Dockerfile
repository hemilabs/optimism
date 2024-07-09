FROM golang:1.22.5-alpine3.20@sha256:8c9183f715b0b4eca05b8b3dbf59766aaedb41ec07477b132ee2891ac0110a07 AS builder

ARG VERSION=v0.0.0 TARGETOS TARGETARCH

RUN apk add git make

WORKDIR /optimism

COPY . /optimism

RUN make op-node VERSION="$VERSION" GOOS=$TARGETOS GOARCH=$TARGETARCH

FROM alpine:3.20@sha256:b89d9c93e9ed3597455c90a0b88a8bbb5cb7188438f70953fede212a0c4394e0

COPY --from=builder /optimism/op-node/bin/op-node /bin/op-node

CMD ['op-node']