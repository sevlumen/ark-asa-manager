const server = Bun.serve({ port: Number(Bun.env.PORT || 3000), fetch: async (request) => {
  const path = new URL(request.url).pathname;
  const file = path === "/" ? Bun.file("./dist/index.html") : Bun.file(`./dist${path}`);
  return (await file.exists()) ? new Response(file) : new Response("Not found", { status: 404 });
}});
console.log(`web listening on ${server.url}`);
