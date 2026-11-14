# syntax=docker/dockerfile:1
# RelayLab auth service (Better Auth). Build from the repository root:
#   docker build -f auth/Dockerfile -t relaylab-auth .
# Commands:
#   node src/main.ts             serve (internal port 3001; reached only via the api proxy)
#   node src/cli/migrate.ts      apply Better Auth migrations (auth_migrator role)
#   node src/cli/create-user.ts  operator: create a verified user (password on stdin)
# Node 24 runs the TypeScript sources directly (type stripping); there is no build step.
# Base images are pinned by digest (multi-arch index). Update them deliberately:
#   docker buildx imagetools inspect <image:tag>

FROM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS deps
RUN npm install -g pnpm@12.16.1
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY auth/package.json auth/
COPY web/package.json web/
COPY backend/package.json backend/
RUN pnpm install --frozen-lockfile --prod --filter @relaylab/auth

FROM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5
ENV NODE_ENV=production
WORKDIR /app
COPY --from=deps --chown=root:root /src/node_modules ./node_modules
COPY --from=deps --chown=root:root /src/auth/node_modules ./auth/node_modules
COPY --chown=root:root auth/package.json auth/
COPY --chown=root:root auth/src auth/src
COPY --chown=root:root deploy/sql deploy/sql
# Apply Alpine security updates newer than the base image (the pinned digest
# can lag a package fix, e.g. zlib CVE-2026-4738). The service runs with node
# alone: drop the package managers bundled in the base image (they are never
# used at runtime and carry their own CVEs).
RUN apk upgrade --no-cache && rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack \
      /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /opt/yarn-* /usr/local/bin/yarn /usr/local/bin/yarnpkg
WORKDIR /app/auth
USER node
EXPOSE 3001
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:3001/readyz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
CMD ["node", "src/main.ts"]
