package muxmaster

import "testing"

// TestJoinPrefix covers specification/groups.md section 11 (requirements
// 41-44) directly against the unexported joinPrefix helper: a white-box,
// exhaustive truth table for the join rule shared by Group route
// registration, sub-group creation, Group.Mount, and Group.ServeFiles.
func TestJoinPrefix(t *testing.T) {
	tests := []struct {
		name        string
		left, right string
		want        string
	}{
		// Requirement 41: left ends with '/', right begins with '/' — drop
		// exactly one slash at the boundary.
		{"trailing-and-leading-slash", "/api/", "/users", "/api/users"},
		{"root-prefix-only", "/", "/users", "/users"},

		// Requirement 42: every other case is a plain concatenation.
		{"no-trailing-slash-leading-slash", "/api", "/users", "/api/users"},
		{"trailing-slash-no-leading-slash", "/api/", "orders", "/api/orders"},
		{"neither-slash", "/api", "users", "/apiusers"},
		{"empty-right", "/api/", "", "/api/"},
		{"empty-left", "", "users", "users"},
		{"empty-left-leading-slash-right", "", "/users", "/users"},

		// Requirement 43: a repeated '/' strictly inside either operand
		// (not at the exact join boundary) is left untouched.
		{"inner-double-slash-in-left", "/api//v1", "/users", "/api//v1/users"},
		{"inner-double-slash-in-right", "/api", "/users//list", "/api/users//list"},
		{"extra-slash-beyond-boundary-preserved", "/api/", "//users", "/api//users"},

		// Requirement 44: '%'-encoded text is ordinary literal text — the
		// join never decodes or special-cases it.
		{"percent-encoded-slash", "/api/", "/%2fusers", "/api/%2fusers"},
		{"percent-literal", "/api", "/100%", "/api/100%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinPrefix(tt.left, tt.right); got != tt.want {
				t.Errorf("joinPrefix(%q, %q) = %q, want %q", tt.left, tt.right, got, tt.want)
			}
		})
	}
}
