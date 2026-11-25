package db

import (
	"regexp"
	"testing"
)

var v7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIDIsV7AndOrdered(t *testing.T) {
	prev := ""
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if !v7.MatchString(id) {
			t.Fatalf("non è un UUIDv7: %s", id)
		}
		if seen[id] {
			t.Fatalf("duplicato: %s", id)
		}
		seen[id] = true
		// Il prefisso di 48 bit è il tempo in ms: non decresce mai.
		if prev != "" && id[:13] < prev[:13] {
			t.Fatalf("ordine temporale violato: %s dopo %s", id, prev)
		}
		prev = id
	}
}
