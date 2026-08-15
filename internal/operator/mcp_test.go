package operator

import "testing"

func TestNormalizeMCPOAuthStatus(t *testing.T) {
	tests := map[string]string{
		"logged_in":         "logged_in",
		"o_auth":            "logged_in",
		" OAuth ":           "logged_in",
		"authenticated":     "logged_in",
		"not_logged_in":     "not_logged_in",
		"not_authenticated": "not_logged_in",
		"unsupported":       "unsupported",
	}
	for input, expected := range tests {
		if actual := normalizeMCPOAuthStatus(input); actual != expected {
			t.Errorf("normalizeMCPOAuthStatus(%q) = %q, want %q", input, actual, expected)
		}
	}
}
