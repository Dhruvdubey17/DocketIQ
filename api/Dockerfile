FROM golang:1.27.1-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /docketiq .

# Distroless has no shell, so the compose healthcheck runs the binary itself.
FROM gcr.io/distroless/static-debian12:nonroot
# The nonroot image defaults to /home/nonroot, and SOURCES_FILE is relative to
# the working directory.
WORKDIR /
COPY --from=build /docketiq /docketiq
COPY --from=build /src/sources.json /sources.json
EXPOSE 8080
ENTRYPOINT ["/docketiq"]
