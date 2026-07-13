FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /trecsd ./cmd/trecsd/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /trecsd /usr/local/bin/trecsd

ENTRYPOINT ["trecsd"]
