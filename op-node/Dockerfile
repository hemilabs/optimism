FROM golang:1.26.5-trixie@sha256:4ee9ffa999b4583ce281939cdff828763083610292f252279a0cee77473bd9a7AS builder

RUN curl https://sh.rustup.rs -sSf | sh -s -- -y
ENV PATH="${PATH}:/root/.cargo/bin"

WORKDIR /git
RUN git clone https://github.com/casey/just
WORKDIR /git/just
# 1.46.0
RUN git checkout f028de5b258a0cc4696b9dea729cc7d4d5828baa
RUN cargo install just

RUN apt update
RUN apt install -y git make bash

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

FROM alpine:3.20@sha256:b89d9c93e9ed3597455c90a0b88a8bbb5cb7188438f70953fede212a0c4394e0

COPY --from=builder /optimism/op-node/bin/op-node /bin/op-node

COPY --from=builder /optimism/op-batcher/bin/op-batcher /bin/op-batcher

COPY --from=builder /optimism/op-proposer/bin/op-proposer /bin/op-proposer

CMD ['op-node']
