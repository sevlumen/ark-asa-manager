FROM oven/bun:1.2-alpine AS build
WORKDIR /app
COPY package.json ./
RUN bun install --frozen-lockfile
COPY . ./
RUN bun run build

FROM oven/bun:1.2-alpine
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY --from=build /app/serve.ts ./serve.ts
USER bun
EXPOSE 3000
CMD ["bun", "run", "serve.ts"]
