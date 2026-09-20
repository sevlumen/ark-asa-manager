const apiOrigin = Bun.env.API_BASE_URL || "http://control-plane:8080";
type ProxySocket = { upstream?: WebSocket; target: string; cookie: string };

const securityHeaders = {
  "Content-Security-Policy": "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self' ws: wss:; form-action 'self'",
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff",
  "X-Frame-Options": "DENY",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=()",
};

function secured(response: Response) {
  const headers = new Headers(response.headers);
  for (const [name, value] of Object.entries(securityHeaders)) headers.set(name, value);
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

const server = Bun.serve<ProxySocket>({ port: Number(Bun.env.PORT || 3000), fetch: async (request, server) => {
  const url = new URL(request.url);
  if (url.pathname === "/api/v1/ws") {
    const target = `${apiOrigin.replace(/^http/, "ws")}${url.pathname}${url.search}`;
    const upgraded = server.upgrade(request, { data: { target, cookie: request.headers.get("cookie") || "" } });
    return upgraded ? undefined : new Response("WebSocket upgrade failed", { status: 400 });
  }
  if (url.pathname.startsWith("/api/") || url.pathname === "/healthz" || url.pathname === "/readyz") {
    const target = `${apiOrigin.replace(/\/$/, "")}${url.pathname}${url.search}`;
    try {
      return secured(await fetch(target, {
        method: request.method,
        headers: request.headers,
        body: request.method === "GET" || request.method === "HEAD" ? undefined : request.body,
      }));
    } catch {
      return secured(Response.json({ error: { code: "upstream_unavailable", message: "Control plane unavailable" } }, { status: 502 }));
    }
  }
  const path = url.pathname === "/" ? "/index.html" : url.pathname;
  const file = Bun.file(`./dist${path}`);
  return secured((await file.exists()) ? new Response(file) : new Response(Bun.file("./dist/index.html")));
}, websocket: {
  open(socket) {
    const upstream = new WebSocket(socket.data.target, { headers: { Cookie: socket.data.cookie } } as any);
    socket.data.upstream = upstream;
    upstream.onmessage = event => socket.send(event.data);
    upstream.onerror = () => socket.close(1011, "control plane unavailable");
    upstream.onclose = event => socket.close(event.code, event.reason);
  },
  message(socket, message) { socket.data.upstream?.send(message); },
  close(socket) { socket.data.upstream?.close(); },
}});
console.log(`web listening on ${server.url}`);
