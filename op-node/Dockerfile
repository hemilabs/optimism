FROM golang:1.24.2-alpine3.21@sha256:7772cb5322baa875edd74705556d08f0eeca7b9c4b5367754ce3f2f00041ccee AS builder

RUN apk add --update git make cargo bash

RUN curl https://sh.rustup.rs -sSf | sh -s -- -y
ENV PATH="${PATH}:/root/.cargo/bin"
RUN echo "export PATH=$PATH" >> /etc/profile

WORKDIR /git
RUN git clone https://github.com/casey/just
WORKDIR /git/just
RUN cargo install just

ARG VERSION=v0.0.0 TARGETOS TARGETARCH

WORKDIR /optimism

COPY . /optimism

SHELL ["bash", "-c"]

WORKDIR /optimism/op-node
RUN just op-node

WORKDIR /optimism/op-batcher
RUN just op-batcher

WORKDIR /optimism/op-proposer
RUN just op-proposer

WORKDIR /optimism/op-conductor
RUN just op-conductor

FROM alpine:3.20@sha256:b89d9c93e9ed3597455c90a0b88a8bbb5cb7188438f70953fede212a0c4394e0

COPY --from=builder /optimism/op-node/bin/op-node /bin/op-node

COPY --from=builder /optimism/op-batcher/bin/op-batcher /bin/op-batcher

COPY --from=builder /optimism/op-proposer/bin/op-proposer /bin/op-proposer

COPY --from=builder /optimism/op-conductor/bin/op-conductor /bin/op-conductor

CMD ['op-node']