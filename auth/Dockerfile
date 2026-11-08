# syntax=docker/dockerfile:1
# RelayLab auth service (Better Auth). Build from the repository root:
#   docker build -f auth/Dockerfile -t relaylab-auth .
# Commands:
#   node src/main.ts             serve (internal port 3001; reached only via the api proxy)
#   node src/cli/migrate.ts      apply Better Auth migrations (auth_migrator role)
#   node src/cli/create-user.ts  operator: create a verified user (password on stdin)
# Node 24 runs the TypeScript sources directly (type stripping); there is no build step.

FROM node:24-alpine AS deps
RUN npm install -g pnpm@12.16.1
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY auth/package.json auth/
COPY web/package.json web/
COPY backend/package.json backend/
RUN pnpm install --frozen-lockfile --prod --filter @relaylab/auth

FROM node:24-alpine
ENV NODE_ENV=production
WORKDIR /app
COPY --from=deps --chown=root:root /src/node_modules ./node_modules
COPY --from=deps --chown=root:root /src/auth/node_modules ./auth/node_modules
COPY --chown=root:root auth/package.json auth/
COPY --chown=root:root auth/src auth/src
COPY --chown=root:root deploy/sql deploy/sql
WORKDIR /app/auth
USER node
EXPOSE 3001
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:3001/readyz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
CMD ["node", "src/main.ts"]
