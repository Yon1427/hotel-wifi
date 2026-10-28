package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

var ErrDuplicateEmail = errors.New("a guest with this email already exists")

type Store struct {
	db *sql.DB
}

type Guest struct {
	ID         string
	Username   string
	Password   string
	Room       string
	GuestName  string
	Status     string
	MaxDevices int
	CheckIn    time.Time
	ExpiresAt  time.Time
	Notes      string
	CreatedAt  time.Time
}

type GuestFilter struct {
	Room   string
	Status string // ACTIVE, DISABLED, or empty
	Expiry string // soon, expired, or empty
}

type Stats struct {
	Active   int
	Expiring int
	Expired  int
}

type Admin struct {
	ID           string
	Username     string
	PasswordHash string
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) ListGuests(ctx context.Context, f GuestFilter) ([]Guest, error) {
	q := `SELECT id::text, username, room, COALESCE(guest_name,''), status,
	             max_devices, check_in, expires_at, COALESCE(notes,''), created_at
	        FROM guests WHERE 1=1`
	args := []any{}
	n := 1
	if room := strings.TrimSpace(f.Room); room != "" {
		q += fmt.Sprintf(" AND room ILIKE $%d", n)
		args = append(args, "%"+room+"%")
		n++
	}
	switch f.Status {
	case "ACTIVE", "DISABLED":
		q += fmt.Sprintf(" AND status = $%d", n)
		args = append(args, f.Status)
		n++
	}
	switch f.Expiry {
	case "soon":
		q += " AND expires_at > now() AND expires_at <= now() + interval '7 days'"
	case "expired":
		q += " AND expires_at <= now()"
	}
	q += " ORDER BY expires_at ASC, room ASC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Guest
	for rows.Next() {
		g, err := scanGuest(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GetGuest(ctx context.Context, id string) (Guest, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id::text, username, password, room,
		COALESCE(guest_name,''), status, max_devices, check_in, expires_at,
		COALESCE(notes,''), created_at FROM guests WHERE id = $1`, id)
	return scanGuest(row, true)
}

func (s *Store) CreateGuest(ctx context.Context, email, room, guestName, password string, expiresAt time.Time, maxDevices int) (Guest, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row := s.db.QueryRowContext(ctx, `INSERT INTO guests
		(username, password, room, guest_name, expires_at, max_devices)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6)
		RETURNING id::text, username, password, room, COALESCE(guest_name,''),
		          status, max_devices, check_in, expires_at, COALESCE(notes,''), created_at`,
		email, password, room, guestName, expiresAt, maxDevices)
	g, err := scanGuest(row, true)
	if err != nil {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Guest{}, ErrDuplicateEmail
		}
		return Guest{}, err
	}
	return g, nil
}

func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	if status != "ACTIVE" && status != "DISABLED" {
		return errors.New("invalid status")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE guests SET status=$2 WHERE id=$1`, id, status)
	return err
}

func (s *Store) UpdateExpiry(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE guests SET expires_at=$2 WHERE id=$1`, id, expiresAt)
	return err
}

func (s *Store) UpdateNotes(ctx context.Context, id, notes string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE guests SET notes=NULLIF($2,'') WHERE id=$1`, id, notes)
	return err
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE status='ACTIVE' AND expires_at > now()),
		COUNT(*) FILTER (WHERE status='ACTIVE' AND expires_at > now() AND expires_at <= now() + interval '7 days'),
		COUNT(*) FILTER (WHERE expires_at <= now())
		FROM guests`).Scan(&st.Active, &st.Expiring, &st.Expired)
	return st, err
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&n)
	return n, err
}

func (s *Store) CreateAdmin(ctx context.Context, username, hash string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_users (username, password_hash) VALUES ($1,$2)`, username, hash)
	return err
}

func (s *Store) GetAdmin(ctx context.Context, username string) (Admin, error) {
	var a Admin
	err := s.db.QueryRowContext(ctx, `SELECT id::text, username, password_hash
		FROM admin_users WHERE username=$1`, username).Scan(&a.ID, &a.Username, &a.PasswordHash)
	return a, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanGuest(row scanner, withPassword bool) (Guest, error) {
	var g Guest
	var err error
	if withPassword {
		err = row.Scan(&g.ID, &g.Username, &g.Password, &g.Room, &g.GuestName,
			&g.Status, &g.MaxDevices, &g.CheckIn, &g.ExpiresAt, &g.Notes, &g.CreatedAt)
	} else {
		err = row.Scan(&g.ID, &g.Username, &g.Room, &g.GuestName,
			&g.Status, &g.MaxDevices, &g.CheckIn, &g.ExpiresAt, &g.Notes, &g.CreatedAt)
	}
	return g, err
}
