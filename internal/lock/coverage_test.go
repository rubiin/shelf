package lock

import "testing"

// TestParseLockFastRejectsUnknownListField drives the fast parser into its
// list-field default so an unrecognized array field falls back to the general
// decoder instead of being silently dropped.
func TestParseLockFastRejectsUnknownListField(t *testing.T) {
	if _, ok := parseLockFast([]byte("[[plugins]]\nname = [\"x\"]\n")); ok {
		t.Fatal("fast parser accepted a plugin text field written as a list")
	}
}
