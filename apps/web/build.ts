import { build } from "bun";

await build({ entrypoints: ["./src/main.tsx"], outdir: "./dist", minify: true, sourcemap: "external" });
await Bun.write("./dist/index.html", await Bun.file("./index.html").text().then((html) => html.replace("/src/main.tsx", "/main.js")));
