package deletion

// Project-scoped tables, registered by the change that creates them.
func init() {
	Register("app.projects", "id = $1") // add-project-management
}
