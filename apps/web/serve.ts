const apiOrigin = Bun.env.API_BASE_URL || "http://control-plane:8080";

const server = Bun.serve({ port: Number(Bun.env.PORT || 3000), fetch: async (request) => {
  const url = new URL(request.url);
  if (url.pathname.startsWith("/api/") || url.pathname === "/healthz" || url.pathname === "/readyz") {
    const target = `${apiOrigin.replace(/\/$/, "")}${url.pathname}${url.search}`;
    try {
      return await fetch(target, {
        method: request.method,
        headers: request.headers,
        body: request.method === "GET" || request.method === "HEAD" ? undefined : request.body,
      });
    } catch {
      return Response.json({ error: { code: "upstream_unavailable", message: "Control plane unavailable" } }, { status: 502 });
    }
  }
  const path = url.pathname === "/" ? "/index.html" : url.pathname;
  const file = Bun.file(`./dist${path}`);
  return (await file.exists()) ? new Response(file) : new Response(Bun.file("./dist/index.html"));
}});
console.log(`web listening on ${server.url}`);
