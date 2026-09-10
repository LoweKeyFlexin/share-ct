# Build stage runs natively on the CI runner and cross-compiles for the target platform
# (linux/arm64 on the Pi). CGO is off: modernc.org/sqlite is pure Go, so no emulation here.
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG COMMIT=unknown
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.commit=$COMMIT -X main.version=$VERSION" \
    -o /out/sharect ./cmd/sharect

# Runtime: static distroless (no shell, no libc, no package manager). Will's compose runs
# it read-only as 2000:2000; USER here makes a plain `docker run` behave the same way.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/sharect /sharect
USER 2000:2000
EXPOSE 8080
# distroless has no curl, so the binary probes itself.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/sharect", "-healthcheck"]
ENTRYPOINT ["/sharect"]
