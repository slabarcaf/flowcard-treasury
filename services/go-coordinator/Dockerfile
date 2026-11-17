FROM golang:1.27.3 AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY . .

RUN mkdir -p /out && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/coordinator-api ./cmd/api && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/coordinator-migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/coordinator-watch ./cmd/watch && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/coordinator-replay ./cmd/replay && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/coordinator-evidence ./cmd/evidence && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/fixture-shortfalls ./cmd/fixture-shortfalls && \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -o /out/prepare-sources ./cmd/prepare-sources && \
    printf 'coordinator:x:65532:65532:Coordinator:/nonexistent:/sbin/nologin\n' > /out/passwd && \
    printf 'coordinator:x:65532:\n' > /out/group

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/passwd /etc/passwd
COPY --from=build /out/group /etc/group
COPY --from=build /out/coordinator-api /usr/local/bin/coordinator-api
COPY --from=build /out/coordinator-migrate /usr/local/bin/coordinator-migrate
COPY --from=build /out/coordinator-watch /usr/local/bin/coordinator-watch
COPY --from=build /out/coordinator-replay /usr/local/bin/coordinator-replay
COPY --from=build /out/coordinator-evidence /usr/local/bin/coordinator-evidence
COPY --from=build /out/fixture-shortfalls /usr/local/bin/fixture-shortfalls
COPY --from=build /out/prepare-sources /usr/local/bin/prepare-sources

USER 65532:65532
EXPOSE 8090 8092
ENTRYPOINT ["/usr/local/bin/coordinator-api"]
