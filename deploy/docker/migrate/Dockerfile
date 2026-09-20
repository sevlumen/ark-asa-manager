FROM golang:1.23-alpine AS build
ENV CGO_ENABLED=0
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.24.2

FROM postgres:16-alpine
COPY --from=build /go/bin/goose /usr/local/bin/goose
ENTRYPOINT ["/usr/local/bin/goose"]
