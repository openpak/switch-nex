# The image CI publishes. Multi-stage and cross-compiled: the build stage runs on
# the runner's native arch and Go emits the target arch, so linux/arm64 needs no
# QEMU — the same property Dockerfile.deploy got by shipping a prebuilt binary.
#
# The runtime stage stays byte-for-byte the shape Dockerfile.deploy produced:
# bare alpine, root, the binary at /app/switch-nexd. Production mounts its certs
# read-only and a state dir for -recall-file, both owned outside the image, so
# this is not the place to start dropping privileges.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
# git so Go can stamp vcs.revision/vcs.modified into the binary: docs/deploy.md
# treats that stamp as the answer to "what is running in production?".
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o /out/switch-nexd ./cmd/switch-nexd

FROM alpine:3.20
# Links the GHCR package to this repository, which is what lets the repo's
# Actions token push to it. Without the link GITHUB_TOKEN gets a 403 on the
# blob check, even with packages:write — the package this repo had was created
# by hand-pushes under a personal token and belonged to no repo (2026-09-26).
LABEL org.opencontainers.image.source=https://github.com/openpak/switch-nex
WORKDIR /app
COPY --from=build /out/switch-nexd /app/switch-nexd
COPY licenses/websocket-ISC.txt /usr/share/licenses/switch-nex/websocket-ISC.txt
ENTRYPOINT ["/app/switch-nexd"]
