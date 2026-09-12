# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=0.2.2 -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o /zte-sr1010-exporter .

# Runtime
FROM alpine:3.19
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.cloud.tencent.com#g' /etc/apk/repositories
RUN apk update
RUN apk add --no-cache ca-certificates
COPY --from=builder /zte-sr1010-exporter /usr/local/bin/zte-sr1010-exporter
EXPOSE 9100
ENTRYPOINT ["/usr/local/bin/zte-sr1010-exporter"]
