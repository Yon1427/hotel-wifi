package mail

import (
	"strings"
	"testing"
	"time"
)

func TestMessage(t *testing.T) {
	tz := time.FixedZone("CET", 3600)
	c := Config{From: "wifi@hotel.test"}
	m := Message{
		To:       "guest@example.com",
		Hotel:    "Hotel",
		Room:     "204",
		Username: "guest@example.com",
		Password: "Ab3dEf9h",
		Expires:  time.Date(2026, 11, 2, 11, 0, 0, 0, tz),
		TZ:       tz,
	}
	b := string(c.message(m))
	for _, want := range []string{
		"To: guest@example.com",
		"Subject: Your Wi-Fi access - Room 204",
		"Username: guest@example.com",
		"Password: Ab3dEf9h",
		"Valid until: 2026-11-02 11:00 CET",
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q in message:\n%s", want, b)
		}
	}
	for _, want := range []string{
		"Content-Type: multipart/alternative; boundary=\"hotel-wifi-alt-1\"",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Type: text/html; charset=UTF-8",
		"<h1 style=",
		"--hotel-wifi-alt-1--",
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q in message", want)
		}
	}
	if strings.Contains(strings.ReplaceAll(b, "\r\n", ""), "\n") {
		t.Fatal("message contains a bare LF")
	}
}

func TestHTMLEscape(t *testing.T) {
	c := Config{From: "wifi@hotel.test"}
	b := string(c.message(Message{
		To:       "guest@example.com",
		Hotel:    "<b>Hotel</b>",
		Room:     "204",
		Username: "guest@example.com",
		Password: "<script>x</script>",
		Expires:  time.Now(),
	}))
	htmlPart := b[strings.Index(b, "Content-Type: text/html"):]
	if strings.Contains(htmlPart, "<script>x</script>") || strings.Contains(htmlPart, "<b>Hotel</b>") {
		t.Fatal("dynamic values are not HTML-escaped")
	}
	if !strings.Contains(htmlPart, "&lt;b&gt;Hotel&lt;/b&gt;") {
		t.Fatal("escaped hotel name missing")
	}
}

func TestHeaderInjection(t *testing.T) {
	c := Config{From: "wifi@hotel.test"}
	b := string(c.message(Message{
		To:       "guest@example.com",
		Hotel:    "Hotel\r\nBcc: attacker@example.com",
		Room:     "204",
		Username: "guest@example.com",
		Password: "Ab3dEf9h",
		Expires:  time.Now(),
	}))
	if strings.Contains(b, "\r\nBcc:") {
		t.Fatal("header injection through hotel name")
	}
}

func TestEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("empty config reported as enabled")
	}
	if !(Config{Host: "smtp.hotel.test", From: "wifi@hotel.test"}).Enabled() {
		t.Fatal("configured relay reported as disabled")
	}
}
