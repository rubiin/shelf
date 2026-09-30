package render

import "testing"

func TestIsPlainNameRejectsEmpty(t *testing.T) {
	if isPlainName("") {
		t.Fatal("isPlainName(\"\") reported an empty name as plain")
	}
	if !isPlainName("demo") {
		t.Fatal("isPlainName(\"demo\") rejected a plain name")
	}
}
