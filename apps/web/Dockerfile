FROM oven/bun:1.2-alpine@sha256:0841c588f6304300baf1d395ae339ce09a6e18c4b6a7cdd4fddcbdb87a2f096a AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . ./
RUN bun run build

FROM oven/bun:1.2-alpine@sha256:0841c588f6304300baf1d395ae339ce09a6e18c4b6a7cdd4fddcbdb87a2f096a
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY --from=build /app/serve.ts ./serve.ts
USER bun
EXPOSE 3000
CMD ["bun", "run", "serve.ts"]
