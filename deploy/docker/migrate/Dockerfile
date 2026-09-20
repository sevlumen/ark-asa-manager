FROM golang:1.23-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f AS build
ENV CGO_ENABLED=0
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.24.2

FROM postgres:16-alpine@sha256:3c5c8892d184f738f4fe282d14ddaa613a38f00f4189d2d94725ebe6f2909ddb
COPY --from=build /go/bin/goose /usr/local/bin/goose
ENTRYPOINT ["/usr/local/bin/goose"]
