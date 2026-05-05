FROM golang:1.25-alpine AS builder
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tvbot ./cmd/tvbot

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/tvbot /app/tvbot
COPY migrations /app/migrations
COPY config/config.yaml.example /app/config/config.yaml.example
EXPOSE 8080
ENV TZ=UTC
ENTRYPOINT ["/app/tvbot"]
