# syntax=docker/dockerfile:1
# One image for both Go binaries. Select the process with the command:
#   /usr/local/bin/api            HTTP server (serves the embedded web app)
#   /usr/local/bin/api migrate    apply migrations (migrator role)
#   /usr/local/bin/worker         background worker

FROM node:24-alpine AS web
RUN npm install -g pnpm@12.16.1
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY backend/package.json backend/
RUN pnpm install --frozen-lockfile --filter @relaylab/web
COPY web/ web/
RUN pnpm --filter @relaylab/web build

FROM golang:1.27-alpine AS go
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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/api /out/worker /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080 8081
CMD ["/usr/local/bin/api"]
