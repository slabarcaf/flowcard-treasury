# Multi-stage reproducible build for company-onboarding API, worker, and migrator.
FROM golang:1.27.3-alpine3.24 AS builder

WORKDIR /src

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree
COPY . ./

# Build statically linked, stripped binaries for API, worker, and migrator
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/migrate ./cmd/migrate

# Minimal, secure, non-root runtime container
FROM alpine:3.21 AS runtime

RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 app && \
    adduser -S -u 10001 -G app -s /sbin/nologin app

COPY --from=builder --chown=10001:10001 /bin/api /bin/api
COPY --from=builder --chown=10001:10001 /bin/worker /bin/worker
COPY --from=builder --chown=10001:10001 /bin/migrate /bin/migrate

USER 10001:10001

EXPOSE 8080

CMD ["/bin/api"]
