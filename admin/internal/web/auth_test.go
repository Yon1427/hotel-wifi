package web

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !verifyPassword(hash, "a-long-enough-password") {
		t.Fatal("valid password rejected")
	}
	if verifyPassword(hash, "wrong-password") {
		t.Fatal("wrong password accepted")
	}
	if verifyPassword("not-a-hash", "a-long-enough-password") {
		t.Fatal("malformed hash accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newSessionStore()
	id, csrf := s.create("alice")
	got, ok := s.get(id)
	if !ok {
		t.Fatal("session missing")
	}
	if got.Username != "alice" {
		t.Fatalf("username = %q", got.Username)
	}
	if got.CSRF != csrf {
		t.Fatal("csrf mismatch")
	}
	s.destroy(id)
	if _, ok := s.get(id); ok {
		t.Fatal("destroyed session still valid")
	}
}

func TestRandomGuestPassword(t *testing.T) {
	pw := randomGuestPassword(8)
	if len(pw) != 8 {
		t.Fatalf("length = %d", len(pw))
	}
}
