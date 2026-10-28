// Package mail sends guest credentials over SMTP.
package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("SMTP is not configured")

// Config is a plain SMTP relay. Mode: starttls (default, port 587),
// implicit (port 465) or none (trusted local relay, no encryption).
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	From     string
	Mode     string
}

func (c Config) Enabled() bool { return c.Host != "" && c.From != "" }

type Message struct {
	To       string
	Hotel    string
	Room     string
	Username string
	Password string
	Expires  time.Time
	TZ       *time.Location
}

func (c Config) Send(m Message) error {
	if !c.Enabled() {
		return ErrNotConfigured
	}
	addr := net.JoinHostPort(c.Host, c.Port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	var err error
	if c.Mode == "implicit" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: c.Host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if c.Mode != "implicit" && c.Mode != "none" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not offer STARTTLS; set SMTP_TLS=none for a trusted relay")
		}
		if err := client.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if c.User != "" {
		if err := client.Auth(smtp.PlainAuth("", c.User, c.Password, c.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(c.From); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(c.message(m)); err != nil {
		w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return client.Quit()
}

func (c Config) message(m Message) []byte {
	tz := m.TZ
	if tz == nil {
		tz = time.UTC
	}
	hotel := m.Hotel
	if hotel == "" {
		hotel = "Hotel"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", header(c.From))
	fmt.Fprintf(&b, "To: %s\r\n", header(m.To))
	fmt.Fprintf(&b, "Subject: Your Wi-Fi access - Room %s\r\n", header(m.Room))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString("Hello,\r\n\r\n")
	fmt.Fprintf(&b, "Your Wi-Fi access for room %s is ready.\r\n\r\n", m.Room)
	fmt.Fprintf(&b, "  Username: %s\r\n", m.Username)
	fmt.Fprintf(&b, "  Password: %s\r\n", m.Password)
	fmt.Fprintf(&b, "  Valid until: %s\r\n\r\n", m.Expires.In(tz).Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(&b, "Connect to the %s Wi-Fi and open any web page to sign in.\r\n\r\n", header(hotel))
	b.WriteString("Front desk\r\n")
	return []byte(b.String())
}

// header strips CR/LF so env values cannot inject extra headers.
func header(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}
