package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	guestmail "hotel/admin/internal/mail"
	"hotel/admin/internal/store"
)

// Mailer delivers guest credentials. *mail.Config satisfies it.
type Mailer interface {
	Send(guestmail.Message) error
}

type Config struct {
	Store         *store.Store
	WebDir        string
	SecureCookies bool
	TrustProxy    bool
	HotelTZ       *time.Location
	Mailer        Mailer
	HotelName     string
}

type Server struct {
	store      *store.Store
	sessions   *sessionStore
	limiter    *loginLimiter
	tmpl       map[string]*template.Template
	tz         *time.Location
	secure     bool
	trustProxy bool
	mailer     Mailer
	hotelName  string
	dummy      string
	webDir     string
}

type baseData struct {
	Title     string
	Admin     string
	CSRF      string
	Flash     string
	Error     string
	ActiveNav string
}

type guestsData struct {
	baseData
	Guests []store.Guest
	Stats  store.Stats
	Filter store.GuestFilter
}

type guestForm struct {
	Email      string
	Room       string
	GuestName  string
	Password   string
	ExpiresAt  string
	MaxDevices string
}

type formData struct {
	baseData
	Form  guestForm
	Error string
}

type detailData struct {
	baseData
	Guest       store.Guest
	ExpiryInput string
	DaysLeft    string
	Expired     bool
}

type loginData struct {
	Title string
	Error string
	User  string
}

func New(cfg Config) (*Server, error) {
	if cfg.HotelTZ == nil {
		cfg.HotelTZ = time.UTC
	}
	if cfg.HotelName == "" {
		cfg.HotelName = "Hotel"
	}
	s := &Server{
		store:      cfg.Store,
		sessions:   newSessionStore(),
		limiter:    newLoginLimiter(),
		tmpl:       make(map[string]*template.Template),
		tz:         cfg.HotelTZ,
		secure:     cfg.SecureCookies,
		trustProxy: cfg.TrustProxy,
		mailer:     cfg.Mailer,
		hotelName:  cfg.HotelName,
		webDir:     cfg.WebDir,
	}
	var err error
	s.dummy, err = HashPassword("dummy-password-for-timing")
	if err != nil {
		return nil, err
	}
	if err := s.loadTemplates(cfg.WebDir); err != nil {
		return nil, err
	}
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.sessions.cleanup()
		}
	}()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("POST /logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /", s.auth(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/guests", http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /guests", s.auth(s.handleGuests))
	mux.HandleFunc("GET /guests/new", s.auth(s.handleGuestNewGet))
	mux.HandleFunc("POST /guests", s.auth(s.handleGuestNewPost))
	mux.HandleFunc("GET /guests/{id}", s.auth(s.handleGuestDetail))
	mux.HandleFunc("POST /guests/{id}/status", s.auth(s.handleGuestStatus))
	mux.HandleFunc("POST /guests/{id}/password", s.auth(s.handleGuestPassword))
	mux.HandleFunc("POST /guests/{id}/expiry", s.auth(s.handleGuestExpiry))
	mux.HandleFunc("POST /guests/{id}/notes", s.auth(s.handleGuestNotes))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(s.webDir, "static")))))
	return s.headers(mux)
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' https://fonts.googleapis.com; "+
				"font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; "+
				"script-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loadTemplates(dir string) error {
	funcs := template.FuncMap{
		"date":      func(t time.Time) string { return t.In(s.tz).Format("Jan 2, 2006") },
		"datetime":  func(t time.Time) string { return t.In(s.tz).Format("Jan 2, 2006 · 15:04") },
		"dateInput": func(t time.Time) string { return t.In(s.tz).Format("2006-01-02T15:04") },
		"upper":     strings.ToUpper,
		"daysLeft":  daysLeft,
		"statusClass": func(g store.Guest) string {
			if g.Status == "DISABLED" {
				return "is-disabled"
			}
			if !g.ExpiresAt.After(time.Now()) {
				return "is-expired"
			}
			return "is-active"
		},
		"initials": func(g store.Guest) string {
			name := strings.TrimSpace(g.GuestName)
			if name == "" {
				return "R" + g.Room
			}
			parts := strings.Fields(name)
			out := ""
			for i, p := range parts {
				if i == 2 {
					break
				}
				out += strings.ToUpper(string([]rune(p)[0]))
			}
			return out
		},
	}
	for _, page := range []string{"guests", "guest_new", "guest_detail"} {
		t, err := template.New("layout").Funcs(funcs).ParseFiles(
			filepath.Join(dir, "templates", "layout.html"),
			filepath.Join(dir, "templates", page+".html"),
		)
		if err != nil {
			return err
		}
		s.tmpl[page] = t
	}
	login, err := template.New("login").Funcs(funcs).ParseFiles(filepath.Join(dir, "templates", "login.html"))
	if err != nil {
		return err
	}
	s.tmpl["login"] = login
	return nil
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.tmpl[page]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := t.ExecuteTemplate(w, pageName(page), data); err != nil {
		slog.Error("render", "page", page, "err", err)
	}
}

func pageName(page string) string {
	if page == "login" {
		return "login"
	}
	return "layout"
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.current(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && r.FormValue("csrf") != sess.CSRF {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) current(r *http.Request) (sessionData, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return sessionData{}, false
	}
	return s.sessions.get(c.Value)
}

func (s *Server) base(r *http.Request, title, nav string) baseData {
	sess, _ := s.current(r)
	return baseData{Title: title, Admin: sess.Username, CSRF: sess.CSRF, ActiveNav: nav}
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.current(r); ok {
		http.Redirect(w, r, "/guests", http.StatusSeeOther)
		return
	}
	s.render(w, "login", loginData{Title: "Sign in"})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin login rejected", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	user := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	key := s.clientIP(r) + "|" + user
	if !s.limiter.allowed(key) {
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, "login", loginData{Title: "Sign in", User: user, Error: "Too many attempts. Try again in 15 minutes."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	a, err := s.store.GetAdmin(ctx, user)
	ok := false
	if err == nil {
		ok = verifyPassword(a.PasswordHash, pass)
	} else {
		_ = verifyPassword(s.dummy, pass) // equalise unknown-user timing
	}
	if !ok {
		s.limiter.fail(key)
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login", loginData{Title: "Sign in", User: user, Error: "Invalid credentials."})
		return
	}
	s.limiter.reset(key)
	id, _ := s.sessions.create(user)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	http.Redirect(w, r, "/guests?welcome=1", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.destroy(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleGuests(w http.ResponseWriter, r *http.Request) {
	f := store.GuestFilter{
		Room:   strings.TrimSpace(r.URL.Query().Get("room")),
		Status: r.URL.Query().Get("status"),
		Expiry: r.URL.Query().Get("expiry"),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	guests, err := s.store.ListGuests(ctx, f)
	if err != nil {
		slog.Error("list guests", "err", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	st, err := s.store.Stats(ctx)
	if err != nil {
		slog.Error("guest stats", "err", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	d := guestsData{baseData: s.base(r, "Guests", "guests"), Guests: guests, Stats: st, Filter: f}
	if r.URL.Query().Get("welcome") == "1" {
		d.Flash = "Signed in. Create a guest account to get started."
	}
	s.render(w, "guests", d)
}

func (s *Server) handleGuestNewGet(w http.ResponseWriter, r *http.Request) {
	d := formData{baseData: s.base(r, "New guest", "new"), Form: guestForm{
		ExpiresAt:  defaultExpiry(s.tz),
		MaxDevices: "2",
	}}
	s.render(w, "guest_new", d)
}

func (s *Server) handleGuestNewPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := guestForm{
		Email:      strings.ToLower(strings.TrimSpace(r.FormValue("email"))),
		Room:       strings.ToUpper(strings.TrimSpace(r.FormValue("room"))),
		GuestName:  strings.TrimSpace(r.FormValue("guest_name")),
		Password:   r.FormValue("password"),
		ExpiresAt:  r.FormValue("expires_at"),
		MaxDevices: strings.TrimSpace(r.FormValue("max_devices")),
	}
	exp, maxDevices, err := validateGuestForm(f, s.tz)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, "guest_new", formData{baseData: s.base(r, "New guest", "new"), Form: f, Error: err.Error()})
		return
	}
	password := f.Password
	if password == "" {
		password = randomGuestPassword(8)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	g, err := s.store.CreateGuest(ctx, f.Email, f.Room, f.GuestName, password, exp, maxDevices)
	if errors.Is(err, store.ErrDuplicateEmail) {
		w.WriteHeader(http.StatusConflict)
		s.render(w, "guest_new", formData{baseData: s.base(r, "New guest", "new"), Form: f, Error: "A guest with this email already exists."})
		return
	}
	if err != nil {
		slog.Error("create guest", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		s.render(w, "guest_new", formData{baseData: s.base(r, "New guest", "new"), Form: f, Error: "Could not create the guest. Try again."})
		return
	}
	sent := s.emailGuest(g, password)
	param := "created=nomail"
	if sent {
		param = "created=mail"
	}
	http.Redirect(w, r, "/guests/"+g.ID+"?"+param, http.StatusSeeOther)
}

// emailGuest sends the guest credentials and reports whether they were accepted.
// A failure never blocks the guest creation; staff can still read the password.
func (s *Server) emailGuest(g store.Guest, password string) bool {
	if s.mailer == nil {
		slog.Warn("guest email not sent: SMTP is not configured", "guest", g.Username)
		return false
	}
	err := s.mailer.Send(guestmail.Message{
		To:       g.Username,
		Hotel:    s.hotelName,
		Room:     g.Room,
		Username: g.Username,
		Password: password,
		Expires:  g.ExpiresAt,
		TZ:       s.tz,
	})
	if err != nil {
		slog.Warn("guest email failed", "guest", g.Username, "err", err)
		return false
	}
	return true
}

func (s *Server) handleGuestDetail(w http.ResponseWriter, r *http.Request) {
	g, err := s.getGuest(r)
	if err != nil {
		s.notFound(w, err)
		return
	}
	d := detailData{
		baseData:    s.base(r, "Room "+g.Room, "guests"),
		Guest:       g,
		ExpiryInput: g.ExpiresAt.In(s.tz).Format("2006-01-02T15:04"),
		DaysLeft:    daysLeft(g.ExpiresAt),
		Expired:     !g.ExpiresAt.After(time.Now()),
	}
	switch r.URL.Query().Get("created") {
	case "mail":
		d.Flash = "Guest created. Credentials emailed to " + g.Username + "."
	case "nomail":
		d.Flash = "Guest created, but the email was not sent. Give the credentials to the guest."
	}
	switch r.URL.Query().Get("rotated") {
	case "mail":
		d.Flash = "Password rotated. New password emailed to " + g.Username + "."
	case "nomail":
		d.Flash = "Password rotated, but the email was not sent. Give the new password to the guest."
	}
	s.render(w, "guest_detail", d)
}

func (s *Server) handleGuestStatus(w http.ResponseWriter, r *http.Request) {
	g, err := s.getGuest(r)
	if err != nil {
		s.notFound(w, err)
		return
	}
	status := "ACTIVE"
	if r.FormValue("action") == "disable" {
		status = "DISABLED"
	}
	if err := s.store.SetStatus(r.Context(), g.ID, status); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/guests/"+g.ID, http.StatusSeeOther)
}

func (s *Server) handleGuestPassword(w http.ResponseWriter, r *http.Request) {
	g, err := s.getGuest(r)
	if err != nil {
		s.notFound(w, err)
		return
	}
	pw := r.FormValue("password")
	if pw == "" {
		pw = randomGuestPassword(8)
	}
	if len(pw) < 8 {
		http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	if err := s.store.RotatePassword(r.Context(), g.ID, pw); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	param := "rotated=nomail"
	if s.emailGuest(g, pw) {
		param = "rotated=mail"
	}
	http.Redirect(w, r, "/guests/"+g.ID+"?"+param, http.StatusSeeOther)
}

func (s *Server) handleGuestExpiry(w http.ResponseWriter, r *http.Request) {
	g, err := s.getGuest(r)
	if err != nil {
		s.notFound(w, err)
		return
	}
	exp, err := time.ParseInLocation("2006-01-02T15:04", r.FormValue("expires_at"), s.tz)
	if err != nil || !exp.After(time.Now()) {
		http.Error(w, "checkout must be a future date and time", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateExpiry(r.Context(), g.ID, exp); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/guests/"+g.ID, http.StatusSeeOther)
}

func (s *Server) handleGuestNotes(w http.ResponseWriter, r *http.Request) {
	g, err := s.getGuest(r)
	if err != nil {
		s.notFound(w, err)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	if len(notes) > 500 {
		http.Error(w, "notes are too long", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateNotes(r.Context(), g.ID, notes); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/guests/"+g.ID, http.StatusSeeOther)
}

func (s *Server) getGuest(r *http.Request) (store.Guest, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	return s.store.GetGuest(ctx, r.PathValue("id"))
}

func (s *Server) notFound(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.Error("get guest", "err", err)
	http.Error(w, "database error", http.StatusInternalServerError)
}

var roomRe = regexp.MustCompile(`^[A-Z0-9-]{1,12}$`)

func validateGuestForm(f guestForm, tz *time.Location) (time.Time, int, error) {
	if _, err := normalizeEmail(f.Email); err != nil {
		return time.Time{}, 0, err
	}
	if !roomRe.MatchString(f.Room) {
		return time.Time{}, 0, errors.New("room must be 1–12 letters, digits or hyphens")
	}
	if len(f.GuestName) > 80 {
		return time.Time{}, 0, errors.New("guest name is too long")
	}
	if f.Password != "" && len(f.Password) < 8 {
		return time.Time{}, 0, errors.New("password must be at least 8 characters")
	}
	exp, err := time.ParseInLocation("2006-01-02T15:04", f.ExpiresAt, tz)
	if err != nil {
		return time.Time{}, 0, errors.New("checkout date and time is invalid")
	}
	if !exp.After(time.Now()) {
		return time.Time{}, 0, errors.New("checkout must be in the future")
	}
	if exp.After(time.Now().AddDate(1, 0, 0)) {
		return time.Time{}, 0, errors.New("checkout must be within one year")
	}
	maxDevices, err := strconv.Atoi(f.MaxDevices)
	if err != nil || maxDevices < 1 || maxDevices > 10 {
		return time.Time{}, 0, errors.New("max devices must be between 1 and 10")
	}
	return exp, maxDevices, nil
}

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) > 254 {
		return "", errors.New("email is too long")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", errors.New("enter a valid guest email address")
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 || parts[0] == "" || !strings.Contains(parts[1], ".") {
		return "", errors.New("enter a valid guest email address")
	}
	return email, nil
}

func defaultExpiry(tz *time.Location) string {
	return time.Now().In(tz).Add(24 * time.Hour).Format("2006-01-02T15:04")
}

func daysLeft(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		days := int(-d.Hours() / 24)
		if days == 0 {
			return "expired today"
		}
		return fmt.Sprintf("expired %d day(s) ago", days)
	}
	days := int(d.Hours() / 24)
	if days == 0 {
		return "expires today"
	}
	if days == 1 {
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", days)
}

func (s *Server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func randomGuestPassword(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}
