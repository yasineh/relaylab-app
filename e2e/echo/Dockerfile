# syntax=docker/dockerfile:1
# Test-only echo destination for the end-to-end suite (docker-compose.e2e.yml).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY main.go .
ENV CGO_ENABLED=0
RUN go mod init echo && go build -trimpath -ldflags="-s -w" -o /echo .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /echo /echo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/echo"]
