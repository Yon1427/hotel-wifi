#!/bin/sh
# Render env-dependent FreeRADIUS configs, then start the daemon.
# Secret charset: printable ASCII without | & \ and no newlines.
set -eu

# FreeRADIUS expands ${...}; keep secrets free of those and of sed metacharacters.
if printf '%s' "${RADIUS_SHARED_SECRET:-}" | grep -qE '[|&\\]|\$\{'; then
	echo "RADIUS_SHARED_SECRET contains characters FreeRADIUS/sed cannot safely render (| & \\ or \${)" >&2
	exit 1
fi

VARS="OPNSENSE_RADIUS_CIDR RADIUS_SHARED_SECRET PGHOST PGPORT POSTGRES_USER POSTGRES_PASSWORD PG_DB_NAME"

render_in_place() {
	file="$1"
	for var in $VARS; do
		val="$(printenv "$var" || true)"
		val_esc="$(printf '%s' "$val" | sed -e 's/[\\|&]/\\&/g')"
		tmp="$file.tmp"
		sed -e "s|\${${var}}|${val_esc}|g" "$file" > "$tmp" && mv "$tmp" "$file"
	done
}

render_in_place /etc/freeradius/clients.conf
render_in_place /etc/freeradius/mods-enabled/sql

exec /usr/sbin/freeradius -f -l /dev/stdout
