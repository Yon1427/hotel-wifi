package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "admin_session"
	sessionTTL    = 8 * time.Hour
	pbkdf2Iter    = 600000
	saltLen       = 16
	hashLen       = 32
	minAdminPass  = 12
)

func HashPassword(pw string) (string, error) {
	if len(pw) < minAdminPass {
		return "", fmt.Errorf("password must be at least %d characters", minAdminPass)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, hashLen)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s", pbkdf2Iter, b64(salt), b64(key)), nil
}

func verifyPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iters, err := strconv.Atoi(parts[2])
	if err != nil || iters < 1 || iters > 10_000_000 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iters, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

type sessionData struct {
	Username string
	CSRF     string
	Expires  time.Time
}

type sessionStore struct {
	mu sync.Mutex
	m  map[string]sessionData
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: make(map[string]sessionData)}
}

func (s *sessionStore) create(username string) (id, csrf string) {
	id = randomToken(32)
	csrf = randomToken(32)
	s.mu.Lock()
	s.m[id] = sessionData{Username: username, CSRF: csrf, Expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	return id, csrf
}

func (s *sessionStore) get(id string) (sessionData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok {
		return sessionData{}, false
	}
	if time.Now().After(v.Expires) {
		delete(s.m, id)
		return sessionData{}, false
	}
	return v, true
}

func (s *sessionStore) destroy(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

func (s *sessionStore) cleanup() {
	s.mu.Lock()
	now := time.Now()
	for id, v := range s.m {
		if now.After(v.Expires) {
			delete(s.m, id)
		}
	}
	s.mu.Unlock()
}

type limiterEntry struct {
	fails int
	until time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	entries  map[string]limiterEntry
	maxFails int
	window   time.Duration
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: make(map[string]limiterEntry), maxFails: 5, window: 15 * time.Minute}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return true
	}
	if time.Now().After(e.until) {
		delete(l.entries, key)
		return true
	}
	return e.fails < l.maxFails
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if time.Now().After(e.until) {
		e = limiterEntry{}
	}
	e.fails++
	e.until = time.Now().Add(l.window)
	l.entries[key] = e
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
