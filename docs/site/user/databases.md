# Databases (User)

`/jabali-panel/databases`. Your MariaDB and PostgreSQL databases.

## Per-row data

- Database name
- Engine (MariaDB / PostgreSQL)
- Default DB user
- Size on disk
- Created

## Database naming

Database names are prefixed with your username for isolation: a database you create with name suffix `wp_site` becomes `<your-username>_wp_site`. The prefix is enforced server-side.

## Adding a database

Click **Create database**, supply:

- Engine (MariaDB or PostgreSQL).
- Name suffix.
- Default DB user — pick an existing DB user or create one in the same wizard. The DB user is granted `ALL PRIVILEGES` on the new database.

The database count is checked against your package's `max_databases`.

## phpMyAdmin / pgAdmin SSO

Each row has an **Open phpMyAdmin** (MariaDB) or **Open pgAdmin** (PostgreSQL) button. Clicking it:

1. Issues a single-use, short-TTL **SSO token** (panel-internal).
2. Redirects to the web admin URL with the token.
3. The web admin authenticates as the corresponding **shadow account** (CONTEXT.md: SSO Token Resolution).
4. The token is consumed and cannot be reused.

You arrive already logged in to phpMyAdmin / pgAdmin with the DB user's privileges.

## Connecting from an application

Your application connects via a Unix socket (the panel runs MariaDB with `skip-networking`; tenant connections happen over the socket):

```
host=/run/mysqld/mysqld.sock
user=<db-user>
password=<password>
dbname=<your-username>_<suffix>
```

For PostgreSQL:

```
host=/var/run/postgresql
user=<db-user>
password=<password>
dbname=<your-username>_<suffix>
```

PHP applications use the same socket path implicitly when host is set to `localhost`.

## Backups

Database content is included in `account_full` backups. To export ad hoc, use phpMyAdmin's **Export** feature (or `pg_dump` for PostgreSQL via pgAdmin's **Backup** tool).

## Deleting a database

Per-row **Delete**. Destructive. The DB user remains (it may own other databases); delete the user separately under [Database Users](./db-users.md) when no databases reference it.

## CLI

If you have shell access (operators only):

```bash
jabali db list --user <your-id>
jabali db create --user <your-id> --name <suffix>
jabali db delete <id>
```
