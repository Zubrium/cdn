# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:20-alpine AS web
RUN corepack enable
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
COPY frontend/admin/package.json ./admin/package.json
COPY frontend/ui/package.json ./ui/package.json
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
COPY pkg/runtime/scripts /src/pkg/runtime/scripts
COPY pkg/runtime/styles /src/pkg/runtime/styles
COPY pkg/runtime/assets /src/pkg/runtime/assets
RUN pnpm build

FROM --platform=$BUILDPLATFORM node:20-alpine AS prerender-deps
WORKDIR /opt/playwright
COPY prerender/package.json prerender/package-lock.json ./
RUN PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm ci --omit=dev --ignore-scripts --no-audit --no-fund

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/pkg/static/dist ./pkg/static/dist
COPY --from=web /src/pkg/runtime/dist ./pkg/runtime/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

FROM node:22-bookworm-slim
RUN apt-get -o Acquire::Retries=3 update \
    && apt-get -o Acquire::Retries=3 install -y --no-install-recommends ca-certificates tzdata ffmpeg chromium wget \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system app \
    && useradd --system --gid app --create-home app \
    && ln -s /usr/local/bin/node /usr/bin/node \
    && mkdir -p /data/releases \
    && chown -R app:app /data
COPY --from=backend /out/server /usr/local/bin/server
COPY --from=prerender-deps /opt/playwright/node_modules /opt/playwright/node_modules
COPY scripts/prerender-worker.mjs /app/scripts/prerender-worker.mjs
USER app
EXPOSE 8080
HEALTHCHECK --interval=20s --timeout=5s --start-period=15s --retries=3 \
    CMD port="${HTTP_ADDR##*:}"; wget -qO- "http://127.0.0.1:${port}/health/ready" >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/server"]
