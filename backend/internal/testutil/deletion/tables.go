package deletion

// Project-scoped tables, registered by the change that creates them.
func init() {
	Register("app.projects", "id = $1")                // add-project-management
	Register("app.endpoints", "project_id = $1")       // add-endpoint-management
	Register("app.audit_log", "project_id = $1")       // add-endpoint-management
	Register("app.webhook_events", "project_id = $1")  // add-webhook-ingestion
	Register("app.jobs", "project_id = $1")            // add-event-replay
	Register("app.replay_attempts", "project_id = $1") // add-event-replay
}
