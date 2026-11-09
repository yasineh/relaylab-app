package deletion

// Project-scoped tables, registered by the change that creates them.
func init() {
	Register("app.projects", "id = $1")          // add-project-management
	Register("app.endpoints", "project_id = $1") // add-endpoint-management
	Register("app.audit_log", "project_id = $1") // add-endpoint-management
}
