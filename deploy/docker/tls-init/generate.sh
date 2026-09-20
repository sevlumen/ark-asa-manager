#!/bin/sh
set -eu

out=/tls
enrollment_out=/enrollment
node_id=${NODE_ID:-local-node}
mkdir -p "$out"
mkdir -p "$enrollment_out"
if [ ! -s "$enrollment_out/ca.pem" ] || [ ! -s "$enrollment_out/ca-key.pem" ]; then
  rm -f "$enrollment_out"/*
  openssl genrsa -out "$enrollment_out/ca-key.pem" 4096
  openssl req -x509 -new -nodes -key "$enrollment_out/ca-key.pem" -sha256 -days 3650 \
    -subj "/CN=ARK ASA enrollment CA" -out "$enrollment_out/ca.pem"
fi
fix_permissions() {
  rm -f "$out/ca-key.pem"
  chown 65532:65532 "$out"/*
  chmod 600 "$out/server-key.pem" "$out/agent-key.pem"
  chown 65532:65532 "$enrollment_out"/*
  chmod 600 "$enrollment_out/ca-key.pem"
}
if [ -s "$out/ca.pem" ] && [ -s "$out/server.pem" ] && [ -s "$out/agent.pem" ]; then
  cp "$enrollment_out/ca.pem" "$out/enrollment-ca.pem"
  fix_permissions
  exit 0
fi
rm -f "$out"/*

openssl genrsa -out "$out/ca-key.pem" 4096
openssl req -x509 -new -nodes -key "$out/ca-key.pem" -sha256 -days 3650 \
  -subj "/CN=ARK ASA local CA" -out "$out/ca.pem"

openssl genrsa -out "$out/server-key.pem" 2048
openssl req -new -key "$out/server-key.pem" -subj "/CN=control-plane" -out "$out/server.csr"
cat > "$out/server.ext" <<EOF
subjectAltName=DNS:control-plane,DNS:localhost
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -in "$out/server.csr" -CA "$out/ca.pem" -CAkey "$out/ca-key.pem" \
  -CAcreateserial -out "$out/server.pem" -days 825 -sha256 -extfile "$out/server.ext"

openssl genrsa -out "$out/agent-key.pem" 2048
openssl req -new -key "$out/agent-key.pem" -subj "/CN=$node_id" -out "$out/agent.csr"
cat > "$out/agent.ext" <<EOF
extendedKeyUsage=clientAuth
EOF
openssl x509 -req -in "$out/agent.csr" -CA "$out/ca.pem" -CAkey "$out/ca-key.pem" \
  -CAcreateserial -out "$out/agent.pem" -days 825 -sha256 -extfile "$out/agent.ext"
rm -f "$out"/*.key "$out"/*.csr "$out"/*.ext "$out"/*.srl
cp "$enrollment_out/ca.pem" "$out/enrollment-ca.pem"
fix_permissions
