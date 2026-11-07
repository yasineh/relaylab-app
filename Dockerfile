# syntax=docker/dockerfile:1
# One image for both Go binaries. Select the process with the command:
#   /usr/local/bin/api            HTTP server (serves the embedded web app)
#   /usr/local/bin/api migrate    apply migrations (migrator role)
#   /usr/local/bin/worker         background worker

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY --from=web /src/web/dist ./internal/webui/dist
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -tags embedweb -o /out/api ./cmd/api \
 && go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/api /out/worker /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080 8081
CMD ["/usr/local/bin/api"]
