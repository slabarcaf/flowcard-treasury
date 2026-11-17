FROM golang:1.27.3-alpine AS build

WORKDIR /workspace

# The funding module uses the committed public underwriting policy module via
# its local ../underwriting-engine replacement. Do not fetch a remote copy.
COPY services/funding-collection/ ./services/funding-collection/
COPY services/underwriting-engine/go.mod services/underwriting-engine/go.sum ./services/underwriting-engine/
COPY services/underwriting-engine/policy/ ./services/underwriting-engine/policy/

WORKDIR /workspace/services/funding-collection
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/funding-api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/funding-worker ./cmd/worker \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/funding-migrate ./cmd/migrate \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/funding-import ./cmd/import \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/funding-demo ./cmd/demo

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/funding-api /usr/local/bin/funding-api
COPY --from=build /out/funding-worker /usr/local/bin/funding-worker
COPY --from=build /out/funding-migrate /usr/local/bin/funding-migrate
COPY --from=build /out/funding-import /usr/local/bin/funding-import
COPY --from=build /out/funding-demo /usr/local/bin/funding-demo

USER nonroot:nonroot
EXPOSE 8085
ENTRYPOINT ["/usr/local/bin/funding-api"]
