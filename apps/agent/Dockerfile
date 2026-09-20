FROM golang:1.23-alpine AS build
WORKDIR /src
COPY apps/agent/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/agent .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agent /agent
USER nonroot:nonroot
EXPOSE 8090
ENTRYPOINT ["/agent"]
