-- Hotel guest Wi-Fi application schema.
-- FreeRADIUS stock tables are in 01-radacct-schema.sql.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS guests (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  username    text UNIQUE NOT NULL,               -- guest email address (RADIUS User-Name)
  password    text NOT NULL,                      -- PAP: FreeRADIUS needs the value
  room        text NOT NULL,
  guest_name  text,
  status      text NOT NULL DEFAULT 'ACTIVE'
              CHECK (status IN ('ACTIVE','DISABLED')),
  max_devices integer NOT NULL DEFAULT 2
              CHECK (max_devices BETWEEN 1 AND 10),
  check_in    timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  notes       text,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_guests_username ON guests(username);
CREATE INDEX IF NOT EXISTS idx_guests_expires  ON guests(expires_at);
CREATE INDEX IF NOT EXISTS idx_guests_room     ON guests(room);

CREATE TABLE IF NOT EXISTS admin_users (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  username      text UNIQUE NOT NULL,
  password_hash text NOT NULL,                    -- pbkdf2$sha256$600000$...
  created_at    timestamptz NOT NULL DEFAULT now()
);
