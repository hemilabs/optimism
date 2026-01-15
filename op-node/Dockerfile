FROM golang:1.24.2@sha256:30baaea08c5d1e858329c50f29fe381e9b7d7bced11a0f5f1f69a1504cdfbf5e AS builder

ENV GOOS=linux

RUN apt update
RUN apt install -y git make bash curl

RUN curl https://sh.rustup.rs -sSf | sh -s -- -y
ENV PATH="${PATH}:/root/.cargo/bin"

WORKDIR /git
RUN git clone https://github.com/casey/just
WORKDIR /git/just
RUN git checkout f028de5b258a0cc4696b9dea729cc7d4d5828baa
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
