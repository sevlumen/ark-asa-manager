FROM golang:1.23-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f AS build
WORKDIR /src
COPY apps/control-plane/go.mod apps/control-plane/go.sum* ./
RUN go mod download
COPY apps/control-plane/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/control-plane .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/control-plane /control-plane
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/control-plane"]
