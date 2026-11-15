FROM golang:1.27.3-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/underwriting-api ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/underwriting-demo ./cmd/demo

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S underwriting \
    && adduser -S -D -H -G underwriting underwriting
WORKDIR /app
COPY --from=build /out/underwriting-api /app/underwriting-api
COPY --from=build /out/underwriting-demo /app/underwriting-demo
COPY demo/entrypoint.sh /app/entrypoint.sh
RUN chmod 0555 /app/entrypoint.sh
USER underwriting
EXPOSE 8083
ENTRYPOINT ["/app/entrypoint.sh"]
