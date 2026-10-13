# syntax=docker/dockerfile:1

# ---- build stage -------------------------------------------------------------
# Full Go toolchain; never shipped. Pin by digest in a real system so a
# re-tagged base image can't change your build silently.
FROM golang:1.27.2-bookworm AS build

WORKDIR /src

# Dependency layer first so it is cached until go.mod/go.sum change.
COPY go.mod go.sum* ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

ARG GIT_SHA=dev
# CGO off -> static binary that runs on distroless/static (no libc).
# -trimpath drops local paths; -s -w strips debug info.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${GIT_SHA}" \
      -o /out/pulse ./cmd/pulse

# ---- runtime stage -----------------------------------------------------------
# No shell, no package manager, no libc: tiny attack surface and few CVEs for
# Trivy to find. The :nonroot tag runs as UID 65532.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/pulse /pulse

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/pulse"]
