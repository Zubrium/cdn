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
ARG BUILD_TAGS=""
RUN CGO_ENABLED=0 go build -trimpath -tags "${BUILD_TAGS}" -o /out/zubrium ./main/cmd/zubrium

# Используем официальный multi-arch образ вместо загрузки через atlasgo.sh:
# installer оказался недоступен из изолированного прогона (broken pipe).
FROM --platform=$BUILDPLATFORM arigaio/atlas:1.3.0 AS atlas

FROM alpine:3.22
RUN addgroup -S zubrium && adduser -S -G zubrium zubrium
COPY --from=build /out/zubrium /usr/local/bin/zubrium
COPY --from=atlas --chmod=0755 /atlas /usr/local/bin/atlas
USER zubrium
ENTRYPOINT ["/usr/local/bin/zubrium"]
