package outbound

// Validator runs the pre-flight URL checks (scheme, userinfo, host, port,
// internal and control-plane names, IP literals) without DNS or network
// access. The API uses it when a replay is created and previewed; DNS and
// socket checks happen when the worker sends.
type Validator struct{ pol policy }

// NewValidator returns a validator with the same policy a Client built from o
// enforces.
func NewValidator(o Options) *Validator {
	return &Validator{pol: newPolicy(o.AllowHTTP, o.AllowedPorts, o.BlockedHosts)}
}

// Validate returns nil when raw passes pre-flight, or the failure.
func (v *Validator) Validate(raw string) *Error {
	_, err := v.pol.validateURL(raw)
	return err
}

// MessageFor returns the user-visible message for a failure category.
func MessageFor(c Category) string { return messages[c] }
