// Package doccheck holds the documentation checks (add-project-documentation):
// relative links and anchors resolve, every API route is documented, the
// ERD matches the migrated schema, and the generated limits block matches
// the configuration defaults. It has no code of its own; its tests run in
// `go test ./...` (and so in `pnpm test` and CI).
package doccheck
