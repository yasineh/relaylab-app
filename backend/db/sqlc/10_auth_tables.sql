-- Used only by sqlc for code generation. The real tables are created by the
-- auth service's Better Auth migrations (`pnpm --filter @relaylab/auth migrate`),
-- not by goose. Only the tables and columns the Go API reads are declared here,
-- exactly as Better Auth 1.7.18 creates them (confirmed by add-user-auth spike 1.1).
-- app_runtime has SELECT on these two tables and nothing else in schema auth.
CREATE TABLE auth."user" (
  id text PRIMARY KEY,
  name text NOT NULL,
  email text NOT NULL UNIQUE,
  "emailVerified" boolean NOT NULL,
  image text,
  "createdAt" timestamptz NOT NULL,
  "updatedAt" timestamptz NOT NULL
);

CREATE TABLE auth."session" (
  id text PRIMARY KEY,
  "expiresAt" timestamptz NOT NULL,
  token text NOT NULL UNIQUE,
  "createdAt" timestamptz NOT NULL,
  "updatedAt" timestamptz NOT NULL,
  "ipAddress" text,
  "userAgent" text,
  "userId" text NOT NULL REFERENCES auth."user" (id) ON DELETE CASCADE
);
