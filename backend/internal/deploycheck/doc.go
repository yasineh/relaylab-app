// Package deploycheck holds offline checks of the deployment definitions in
// deploy/ and .github/workflows/deploy.yml (add-production-readiness). It has
// no code of its own: `deploy/render.sh --check` runs its tests, which render
// the Cloud Run manifests with placeholder values and validate them without
// any cloud call or credential.
package deploycheck
