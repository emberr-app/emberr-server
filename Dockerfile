FROM golang:1.26.9-alpine3.23 AS builder
ENV CGO_ENABLED=0

WORKDIR /etc/emberr/

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
  go build -trimpath -o server ./cmd/server

FROM alpine:3.22
COPY --from=builder /etc/emberr/server .
COPY migrations migrations

CMD ["./server"]
