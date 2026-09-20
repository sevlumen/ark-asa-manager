FROM alpine:3.20
RUN apk add --no-cache openssl
COPY deploy/docker/tls-init/generate.sh /usr/local/bin/generate.sh
RUN chmod +x /usr/local/bin/generate.sh
ENTRYPOINT ["/usr/local/bin/generate.sh"]
