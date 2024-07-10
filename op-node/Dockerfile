FROM golang:1.22.5-alpine3.20@sha256:8c9183f715b0b4eca05b8b3dbf59766aaedb41ec07477b132ee2891ac0110a07 AS builder

ARG VERSION=v0.0.0 TARGETOS TARGETARCH

RUN apk add git make

WORKDIR /optimism

COPY . .

RUN make op-node VERSION="$VERSION" GOOS=$TARGETOS GOARCH=$TARGETARCH

FROM alpine:3.20@sha256:b89d9c93e9ed3597455c90a0b88a8bbb5cb7188438f70953fede212a0c4394e0

ARG VERSION
ARG VCS_REF
ARG BUILD_DATE
LABEL org.opencontainers.image.created=$BUILD_DATE \
      org.opencontainers.image.authors="Hemi Labs" \
      org.opencontainers.image.url="https://github.com/hemilabs/optimism/op-node" \
      org.opencontainers.image.source="https://github.com/hemilabs/optimism" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$VCS_REF \
      org.opencontainers.image.vendor="Hemi Labs" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.title="Optimism Op-Node" \
      org.label-schema.build-date=$BUILD_DATE \
      org.label-schema.name="Optimism Op-Node" \
      org.label-schema.url="https://github.com/hemilabs/optimism/op-node" \
      org.label-schema.vcs-url="https://github.com/hemilabs/optimism" \
      org.label-schema.vcs-ref=$VCS_REF \
      org.label-schema.vendor="Hemi Labs" \
      org.label-schema.version=$VERSION \
      org.label-schema.schema-version="1.0"

COPY --from=builder /optimism/op-node/bin/op-node /bin/op-node

CMD ["op-node"]