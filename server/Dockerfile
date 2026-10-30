# ─── Build stage ───
FROM golang:1.27-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/server ./cmd/server
# The operator's moderation tool (docs/MODERATION.md): a static binary that only
# talks to the server's control socket, run with
# `kubectl exec deploy/<server> -- /moderate <command>` (the image has no shell).
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/moderate ./cmd/moderate

# ─── Production stage ───
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/server /server
COPY --from=builder /out/moderate /moderate

ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/server"]
