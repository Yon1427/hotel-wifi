// Package mail sends guest credentials over SMTP.
package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html"
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

const boundary = "hotel-wifi-alt-1"

func (c Config) message(m Message) []byte {
	tz := m.TZ
	if tz == nil {
		tz = time.UTC
	}
	hotel := m.Hotel
	if hotel == "" {
		hotel = "Hotel"
	}
	until := m.Expires.In(tz).Format("2 Jan 2006, 15:04 MST")

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\n", header(c.From))
	fmt.Fprintf(&b, "To: %s\n", header(m.To))
	fmt.Fprintf(&b, "Subject: Your Wi-Fi access - Room %s\n", header(m.Room))
	fmt.Fprintf(&b, "Date: %s\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\n\n", boundary)

	fmt.Fprintf(&b, "--%s\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\n\n")
	fmt.Fprintf(&b, "Hello,\n\n")
	fmt.Fprintf(&b, "Your Wi-Fi access for room %s is ready.\n\n", header(m.Room))
	fmt.Fprintf(&b, "  Username: %s\n", header(m.Username))
	fmt.Fprintf(&b, "  Password: %s\n", header(m.Password))
	fmt.Fprintf(&b, "  Valid until: %s\n\n", m.Expires.In(tz).Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(&b, "Connect to the %s Wi-Fi and open any web page to sign in.\n\n", header(hotel))
	b.WriteString("Front desk\n")

	fmt.Fprintf(&b, "--%s\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\n\n")
	b.WriteString(htmlBody(hotel, m.Room, m.Username, m.Password, until))
	fmt.Fprintf(&b, "\n--%s--\n", boundary)

	return []byte(strings.ReplaceAll(b.String(), "\n", "\r\n"))
}

// htmlBody follows the portal style: navy header, paper card, serif headline.
func htmlBody(hotel, room, username, password, until string) string {
	hotel, room = esc(hotel), esc(room)
	username, password, until = esc(username), esc(password), esc(until)
	return `<!doctype html>
<html lang="en" style="color-scheme:light only;">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light only">
<meta name="supported-color-schemes" content="light only">
<title>Your Wi-Fi access</title>
</head>
<body style="margin:0;padding:0;background:#f6f1e8;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">Your Wi-Fi access for room ` + room + ` is ready.</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f6f1e8;">
<tr><td align="center" style="padding:28px 16px;">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background:#fffdf9;border:1px solid #e2d8c9;border-radius:18px;">
<tr><td style="background:#050033;padding:26px 30px;border-radius:17px 17px 0 0;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
<td width="42" height="42" align="center" valign="middle" style="width:42px;height:42px;border:1px solid rgba(255,255,255,0.28);border-radius:12px;color:#ffb59f;font-family:Georgia,'Times New Roman',serif;font-size:20px;line-height:42px;">&#10022;</td>
<td style="padding-left:12px;color:#ffffff;font-family:Georgia,'Times New Roman',serif;font-size:26px;line-height:1;">` + hotel + `<br><span style="font-family:Arial,Helvetica,sans-serif;font-size:10px;font-weight:bold;letter-spacing:2px;color:#b8afa0;">HOTEL GUEST WI-FI</span></td>
</tr></table>
</td></tr>
<tr><td style="padding:34px 30px 0;">
<p style="margin:0 0 10px;font-family:Arial,Helvetica,sans-serif;font-size:11px;font-weight:bold;letter-spacing:2px;text-transform:uppercase;color:#da300a;">Hotel guest Wi-Fi</p>
<h1 style="margin:0;font-family:Georgia,'Times New Roman',serif;font-size:30px;line-height:1.15;font-weight:normal;color:#101828;">Your access is ready.</h1>
<p style="margin:14px 0 0;font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.6;color:#5d6470;">Room <strong style="color:#101828;">` + room + `</strong> &mdash; sign in with the details below.</p>
</td></tr>
<tr><td style="padding:22px 30px 0;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f6f1e8;border:1px solid #e2d8c9;border-radius:14px;">
<tr><td style="padding:18px 20px;">
<p style="margin:0;font-family:Arial,Helvetica,sans-serif;font-size:10px;font-weight:bold;letter-spacing:1.6px;text-transform:uppercase;color:#5d6470;">Email</p>
<p style="margin:4px 0 16px;font-family:Arial,Helvetica,sans-serif;font-size:16px;line-height:1.4;color:#101828;word-break:break-all;">` + username + `</p>
<p style="margin:0;font-family:Arial,Helvetica,sans-serif;font-size:10px;font-weight:bold;letter-spacing:1.6px;text-transform:uppercase;color:#5d6470;">Password</p>
<p style="margin:4px 0 0;font-family:'Courier New',Courier,monospace;font-size:22px;font-weight:bold;letter-spacing:2px;color:#050033;word-break:break-all;">` + password + `</p>
</td></tr>
</table>
</td></tr>
<tr><td style="padding:16px 30px 0;">
<p style="margin:0;font-family:Arial,Helvetica,sans-serif;font-size:14px;line-height:1.6;color:#5d6470;"><span style="color:#0f6b4f;font-weight:bold;">&#10003;</span> Valid until <strong style="color:#101828;">` + until + `</strong></p>
</td></tr>
<tr><td style="padding:16px 30px 0;">
<p style="margin:0;font-family:Arial,Helvetica,sans-serif;font-size:14px;line-height:1.6;color:#5d6470;">Connect to the <strong style="color:#101828;">` + hotel + `</strong> Wi-Fi, then open any web page to reach the sign-in page.</p>
</td></tr>
<tr><td style="padding:26px 30px 30px;">
<div style="border-top:1px solid #e2d8c9;padding-top:18px;">
<p style="margin:0;font-family:Arial,Helvetica,sans-serif;font-size:12px;line-height:1.6;color:#5d6470;">Need help? Call reception.<br><span style="color:#8a8275;">` + hotel + ` &middot; Secure guest access</span></p>
</div>
</td></tr>
</table>
<p style="margin:14px 0 0;font-family:Arial,Helvetica,sans-serif;font-size:10px;line-height:1.5;color:#8a8275;">This message was sent by the front desk. Do not forward it.</p>
</td></tr>
</table>
</body>
</html>`
}

func esc(v string) string {
	return html.EscapeString(header(v))
}

// header strips CR/LF so env values cannot inject extra headers.
func header(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}
