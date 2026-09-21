#!/usr/bin/env python3
"""Small Source RCON client used only during the runtime shutdown hook."""

import os
import socket
import struct
import sys


def packet(packet_id: int, packet_type: int, body: str) -> bytes:
    payload = struct.pack("<ii", packet_id, packet_type) + body.encode() + b"\x00\x00"
    return struct.pack("<i", len(payload)) + payload


def read_packet(sock: socket.socket) -> tuple[int, int, str]:
    header = sock.recv(4)
    if len(header) != 4:
        raise RuntimeError("RCON closed before response")
    size = struct.unpack("<i", header)[0]
    if size < 10 or size > 1024 * 1024:
        raise RuntimeError("invalid RCON response size")
    payload = b""
    while len(payload) < size:
        chunk = sock.recv(size - len(payload))
        if not chunk:
            raise RuntimeError("RCON closed during response")
        payload += chunk
    packet_id, packet_type = struct.unpack("<ii", payload[:8])
    body = payload[8:-2].decode(errors="replace")
    return packet_id, packet_type, body


def main() -> int:
    if len(sys.argv) != 4:
        print("usage: rcon_client.py host port command", file=sys.stderr)
        return 2
    password = os.environ.get("ARK_RCON_PASSWORD", "")
    if not password:
        print("RCON password is not configured", file=sys.stderr)
        return 3
    host, port_text, command = sys.argv[1:]
    try:
        port = int(port_text)
    except ValueError:
        print("invalid RCON port", file=sys.stderr)
        return 2
    with socket.create_connection((host, port), timeout=3) as sock:
        sock.settimeout(3)
        sock.sendall(packet(1, 3, password))
        auth_id, _, _ = read_packet(sock)
        if auth_id == -1:
            print("RCON authentication failed", file=sys.stderr)
            return 4
        sock.sendall(packet(2, 2, command))
        response_id, _, _ = read_packet(sock)
        if response_id != 2:
            print("RCON command was not acknowledged", file=sys.stderr)
            return 5
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
