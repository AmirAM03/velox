// Package checks defines the built-in profile types. Each type knows how to
// test a node for one service (its bench.Check) and which settings, such as a
// session credential, it needs. Velox has exactly one profile per type.
package checks

import "github.com/AmirAM03/velox/internal/bench"

// Field is one setting a profile type needs, shown in the profile form.
type Field struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Help   string `json:"help"`
	Secret bool   `json:"secret"` // never sent back to the dashboard
}

// Kind is a built-in profile type.
type Kind struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	Login       *OAuth  `json:"login,omitempty"` // nil: the type has no account sign-in
	// New validates the settings and builds a fresh check for one test run.
	New func(settings map[string]string) (bench.Check, error) `json:"-"`
}

// Kinds lists every profile type, in dashboard order. Add a new service here.
var Kinds = []Kind{antigravityKind}

// Get returns the profile type with the given ID.
func Get(id string) (Kind, bool) {
	for _, k := range Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}
