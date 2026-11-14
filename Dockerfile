# syntax=docker/dockerfile:1
# One image for both Go binaries. Select the process with the command:
#   /usr/local/bin/api            HTTP server (serves the embedded web app)
#   /usr/local/bin/api migrate    apply migrations (migrator role)
#   /usr/local/bin/worker         background worker
#   /usr/local/bin/api healthcheck [url]   liveness probe (HEALTHCHECK below; the
#                                 worker's Compose service passes its own URL)
# Base images are pinned by digest (multi-arch index). Update them deliberately:
#   docker buildx imagetools inspect <image:tag>

FROM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS web
RUN npm install -g pnpm@12.16.1
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY backend/package.json backend/
COPY auth/package.json auth/
RUN pnpm install --frozen-lockfile --filter @relaylab/web
COPY web/ web/
RUN pnpm --filter @relaylab/web build

FROM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS go
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/cmd ./cmd
COPY backend/internal ./internal
COPY backend/migrations ./migrations
COPY --from=web /src/web/dist ./internal/webui/dist
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -tags embedweb -o /out/api ./cmd/api \
 && go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=go /out/api /out/worker /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080 8081
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/usr/local/bin/api", "healthcheck"]
CMD ["/usr/local/bin/api"]
