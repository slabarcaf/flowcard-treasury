FROM golang:1.27.3-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -o /bin/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /bin/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /bin/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /bin/replay ./cmd/replay

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 app && \
    adduser -S -u 10001 -G app -s /sbin/nologin app
COPY --from=builder /bin/api /bin/worker /bin/migrate /bin/replay /bin/
USER 10001:10001
EXPOSE 8082
CMD ["/bin/api"]
