package web

import (
	"testing"
	"time"
)

func TestValidateGuestForm(t *testing.T) {
	expiry := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02T15:04")
	base := guestForm{Email: "guest@example.com", Room: "204", ExpiresAt: expiry, MaxDevices: "2"}

	if _, _, err := validateGuestForm(base, time.UTC); err != nil {
		t.Fatalf("valid form rejected: %v", err)
	}
	if _, _, err := validateGuestForm(guestForm{Email: "not-an-email", Room: "204", ExpiresAt: expiry, MaxDevices: "2"}, time.UTC); err == nil {
		t.Fatal("invalid email accepted")
	}
	if _, _, err := validateGuestForm(guestForm{Email: "guest@example.com", Room: "20 4", ExpiresAt: expiry, MaxDevices: "2"}, time.UTC); err == nil {
		t.Fatal("invalid room accepted")
	}
	if _, _, err := validateGuestForm(guestForm{Email: "guest@example.com", Room: "204", ExpiresAt: "2000-01-01T00:00", MaxDevices: "2"}, time.UTC); err == nil {
		t.Fatal("past checkout accepted")
	}
	if _, _, err := validateGuestForm(guestForm{Email: "guest@example.com", Room: "204", ExpiresAt: expiry, MaxDevices: "0"}, time.UTC); err == nil {
		t.Fatal("zero devices accepted")
	}
}
