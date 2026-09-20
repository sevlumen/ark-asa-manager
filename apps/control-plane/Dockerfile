FROM golang:1.23-alpine AS build
WORKDIR /src
COPY apps/control-plane/go.mod apps/control-plane/go.sum* ./
RUN go mod download
COPY apps/control-plane/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/control-plane .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/control-plane /control-plane
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/control-plane"]
