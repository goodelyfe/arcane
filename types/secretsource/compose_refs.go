package secretsource

// Compose references make a secret reach a container: compose only passes a
// variable to a service that names it, such as DB_PASSWORD: ${DB_PASSWORD}.

// ComposeService is one service of a compose file.
type ComposeService struct {
	Name string `json:"name"`
	// Available are keys the service already gets, through its environment
	// block or by interpolating them anywhere.
	Available []string `json:"available"`
	// Editable is false when Arcane cannot add references to the service as
	// text; Reason says why.
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"`
}

type ComposeServicesRequest struct {
	Compose string `json:"compose"`
}

type ComposeRefsRequest struct {
	Compose string `json:"compose"`
	// Assignments maps a service name to the keys to add to it.
	Assignments map[string][]string `json:"assignments"`
}

// ComposeSkippedRef is a key that was not added to a service.
type ComposeSkippedRef struct {
	Service string `json:"service"`
	Key     string `json:"key"`
	Reason  string `json:"reason"`
}

type ComposeRefsResult struct {
	Compose string              `json:"compose"`
	Added   map[string][]string `json:"added"`
	Skipped []ComposeSkippedRef `json:"skipped"`
}

type TargetKeysRequest struct {
	Target BindingTarget `json:"target"`
}

// TargetKeys are the variable names a target provides. Values are never
// returned.
type TargetKeys struct {
	Keys []string `json:"keys"`
	// Skipped are entries that cannot be used as variables.
	Skipped []string `json:"skipped"`
}
