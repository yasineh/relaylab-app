package outbound

// canaries must always be blocked. The self-check runs them through the same
// pre-flight and socket checks real requests use, without touching the
// network (they are IP literals or names blocked before resolution).
var canaries = []string{
	"https://127.0.0.1/",
	"https://[::1]/",
	"https://169.254.169.254/computeMetadata/v1/",
	"https://metadata.google.internal/computeMetadata/v1/",
	"https://10.0.0.1/",
}

// canarySockets must be refused by the dial-time check.
var canarySockets = []string{"127.0.0.1:443", "[::1]:443", "169.254.169.254:443", "10.0.0.1:443"}

// selfCheck reports whether every canary is blocked. Any canary that passes
// means the policy is broken, and the client must fail closed.
func (c *Client) selfCheck() bool {
	ok := true
	for _, u := range canaries {
		if _, err := c.pol.validateURL(u); err == nil {
			c.logger.Error("outbound guard canary passed pre-flight validation", "canary", u)
			ok = false
		}
	}
	for _, a := range canarySockets {
		if err := c.control("tcp", a, nil); err == nil {
			c.logger.Error("outbound guard canary passed the dial-time check", "canary", a)
			ok = false
		}
	}
	return ok
}
