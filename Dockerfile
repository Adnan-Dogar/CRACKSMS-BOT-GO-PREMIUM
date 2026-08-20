FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go test ./... && \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/cracksms-bot ./cmd/bot && \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/cracksms-importer ./cmd/importer

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*
RUN useradd --system --uid 10001 --create-home cracksms
COPY --from=build /out/cracksms-bot /usr/local/bin/cracksms-bot
COPY --from=build /out/cracksms-importer /usr/local/bin/cracksms-importer
USER cracksms
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cracksms-bot"]
