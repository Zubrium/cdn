# Experimental composition; selected modules must have registered production factories.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.work ./
COPY core ./core
COPY engines ./engines
COPY presentation ./presentation
COPY workers ./workers
COPY pkg ./pkg
COPY main ./main
RUN CGO_ENABLED=0 go build -trimpath -o /out/zubrium ./main/cmd/zubrium

FROM --platform=$BUILDPLATFORM alpine:3.22 AS atlas
RUN apk add --no-cache ca-certificates curl
ARG TARGETARCH
ARG ATLAS_VERSION=v1.3.0
ENV ATLAS_VERSION=${ATLAS_VERSION} CI=true
RUN curl -fsSL https://atlasgo.sh | sh -s -- --community --platform "linux-${TARGETARCH}" --output /out/atlas --no-install

FROM alpine:3.22
RUN addgroup -S zubrium && adduser -S -G zubrium zubrium
COPY --from=build /out/zubrium /usr/local/bin/zubrium
COPY --from=atlas --chmod=0755 /out/atlas /usr/local/bin/atlas
USER zubrium
ENTRYPOINT ["/usr/local/bin/zubrium"]
