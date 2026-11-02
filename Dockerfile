FROM golang:1.26-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -o /centerlinecrm ./cmd/server

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S crm && adduser -S crm -G crm && mkdir /data && chown crm:crm /data
COPY --from=builder /centerlinecrm /usr/local/bin/centerlinecrm
USER crm
VOLUME ["/data"]
EXPOSE 3100
ENTRYPOINT ["centerlinecrm"]
CMD ["--database", "/data/centerlinecrm.db", "--address", "0.0.0.0:3100"]
