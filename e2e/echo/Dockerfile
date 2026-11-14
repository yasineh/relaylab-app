# syntax=docker/dockerfile:1
# Test-only echo destination for the end-to-end suite (docker-compose.e2e.yml).
FROM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS build
WORKDIR /src
COPY main.go .
ENV CGO_ENABLED=0
RUN go mod init echo && go build -trimpath -ldflags="-s -w" -o /echo .

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /echo /echo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/echo"]
