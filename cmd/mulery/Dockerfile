# Build a go app into a minimal docker image with timezone support and SSL cert chains.
FROM golang:latest@sha256:3fccedea46315261e4b6205bcffe91ece1e2aea60c23aab0f033f35461849b42 as builder
ARG TARGETOS
ARG TARGETARCH
ARG BUILD_FLAGS=""

RUN mkdir -p $GOPATH/pkg/mod $GOPATH/bin $GOPATH/src /build
COPY . /build
WORKDIR /build

RUN apt update \
    && apt install -y tzdata ca-certificates \
    && GOOS=${TARGETOS} GOARCH=${TARGETARCH} CGO_ENABLED=0 \
       go build ${BUILD_FLAGS} -o /mulery .

FROM scratch
COPY --from=builder /mulery /mulery
# Make sure we have an ssl cert chain and timezone data.
COPY --from=builder /etc/ssl /etc/ssl
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

ENV TZ=UTC

ENTRYPOINT [ "/mulery" ]
