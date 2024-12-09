FROM golang:1.22.5-alpine3.20@sha256:8c9183f715b0b4eca05b8b3dbf59766aaedb41ec07477b132ee2891ac0110a07 AS builder

RUN apk add --update git make

ARG VERSION=v0.0.0 TARGETOS TARGETARCH

WORKDIR /
RUN git clone https://github.com/hemilabs/op-geth
WORKDIR /op-geth
RUN git checkout 76454bc

WORKDIR /optimism

COPY . /optimism

RUN VERSION="$VERSION" GOOS=$TARGETOS GOARCH=$TARGETARCH make op-node op-batcher op-proposer op-conductor
RUN VERSION="$VERSION" GOOS=$TARGETOS GOARCH=$TARGETARCH make -C ./op-conductor op-conductor

FROM alpine:3.20@sha256:b89d9c93e9ed3597455c90a0b88a8bbb5cb7188438f70953fede212a0c4394e0

COPY --from=builder /optimism/op-node/bin/op-node /bin/op-node

COPY --from=builder /optimism/op-batcher/bin/op-batcher /bin/op-batcher

COPY --from=builder /optimism/op-proposer/bin/op-proposer /bin/op-proposer

COPY --from=builder /optimism/op-conductor/bin/op-conductor /bin/op-conductor

CMD ['op-node']