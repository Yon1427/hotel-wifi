FROM freeradius/freeradius-server:3.2.10

# radtest/radclient for operational debugging and the test matrix.
RUN apt-get update \
 && apt-get install -y --no-install-recommends freeradius-utils \
 && rm -rf /var/lib/apt/lists/*

COPY clients.conf /etc/freeradius/clients.conf
COPY mods-enabled/sql /etc/freeradius/mods-enabled/sql
COPY sql-queries.conf /etc/freeradius/sql-queries.conf
COPY sites-enabled/default /etc/freeradius/sites-enabled/default
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
