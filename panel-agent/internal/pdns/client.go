// Package pdns is a typed SQL client for the PowerDNS backend schema.
// It talks directly to the jabali_pdns database that install.sh
// provisioned. One package-level global *Client is initialised at
// agent startup by ReadEnvAndConnect(); handlers look it up via the
// Default() accessor.
package pdns

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
)

type Client struct {
	db *sql.DB
}

var (
	defaultMu sync.RWMutex
	defaultCl *Client
)

// Default returns the agent-wide client, or nil if ReadEnvAndConnect
// hasn't been called or failed. Handlers check for nil and return a
// friendly error instead of panicking.
func Default() *Client {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultCl
}

// SetDefault is called from the agent bootstrap once we have a client.
func SetDefault(c *Client) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultCl = c
}

// ReadEnvAndConnect parses /etc/jabali-panel/pdns.env, opens a MySQL
// connection, and returns a ready Client. Returns (nil, err) cleanly
// when the env file is missing so the agent boots even on dev boxes
// without PowerDNS.
func ReadEnvAndConnect() (*Client, error) {
	envPath := os.Getenv("JABALI_PDNS_ENV_FILE")
	if envPath == "" {
		envPath = "/etc/jabali-panel/pdns.env"
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", envPath, err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"")
	}
	name, user, pass := env["PDNS_DB_NAME"], env["PDNS_DB_USER"], env["PDNS_DB_PASSWORD"]
	if name == "" || user == "" || pass == "" {
		return nil, fmt.Errorf("%s missing PDNS_DB_NAME/USER/PASSWORD", envPath)
	}
	// M25 Step 6: dial MariaDB over its Debian-default Unix socket. The
	// agent already runs on the same host as MariaDB by definition (it's
	// the host-mutation daemon); a TCP loopback round-trip was overhead
	// rather than capability. Format is the native go-sql-driver/mysql
	// `user:pass@unix(/path)/db?...` form.
	dsn := fmt.Sprintf("%s:%s@unix(/var/run/mysqld/mysqld.sock)/%s?parseTime=true&charset=utf8mb4",
		user, pass, name)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Client{db: db}, nil
}

// Close releases the connection pool. Tests use it; production holds
// the client for the process lifetime.
func (c *Client) Close() error {
	return c.db.Close()
}

// Record mirrors the fields we write. Serial is bumped per zone push
// and handled outside this struct.
type Record struct {
	Name     string
	Type     string
	Content  string
	TTL      int
	Priority int
	Disabled bool
}

// UpsertZoneOptions carries per-zone metadata updated alongside the
// record set. Zero values are meaningful: empty slice clears the
// metadata, which is the right behavior when an operator removes ns2.
type UpsertZoneOptions struct {
	AllowAXFRFrom []string // IPs permitted to pull the zone (ns2_ipv4 when set)
	AlsoNotify    []string // NOTIFY targets on zone change (usually ns2_ipv4)
}

// upsertZoneCore is the common logic shared by UpsertZone and
// UpsertZoneWithMeta. It handles zone lookup/creation and record
// upsert. The caller is responsible for the transaction.
func (c *Client) upsertZoneCore(tx *sql.Tx, name string, records []Record) (int64, error) {
	// Find or create the zone row. PowerDNS's unique key is `name`.
	var zoneID int64
	err := tx.QueryRow(`SELECT id FROM domains WHERE name = ?`, name).Scan(&zoneID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(
			`INSERT INTO domains (name, type) VALUES (?, 'NATIVE')`, name)
		if err != nil {
			return 0, fmt.Errorf("insert zone: %w", err)
		}
		zoneID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, fmt.Errorf("select zone: %w", err)
	}

	// Wipe and rewrite the record set. Cheap because a typical zone
	// has <50 records. Skipping this for "smart" partial updates would
	// drift PowerDNS from panel DB if records were dropped client-side.
	if _, err := tx.Exec(`DELETE FROM records WHERE domain_id = ?`, zoneID); err != nil {
		return 0, fmt.Errorf("clear records: %w", err)
	}
	ins, err := tx.Prepare(`INSERT INTO records
		(domain_id, name, type, content, ttl, prio, disabled, auth)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)`)
	if err != nil {
		return 0, err
	}
	defer ins.Close()
	for _, r := range records {
		disabled := 0
		if r.Disabled {
			disabled = 1
		}
		if _, err := ins.Exec(zoneID, r.Name, r.Type, r.Content, r.TTL, r.Priority, disabled); err != nil {
			return 0, fmt.Errorf("insert %s %s: %w", r.Name, r.Type, err)
		}
	}
	return zoneID, nil
}

// setZoneMetadata replaces the given kind's rows for the zone. Empty
// list clears the kind entirely. Runs inside a caller-provided tx so
// the record write and metadata write are atomic.
func (c *Client) setZoneMetadata(tx *sql.Tx, zoneID int64, kind string, contents []string) error {
	if _, err := tx.Exec(`DELETE FROM domainmetadata WHERE domain_id = ? AND kind = ?`, zoneID, kind); err != nil {
		return fmt.Errorf("clear metadata %s: %w", kind, err)
	}
	if len(contents) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO domainmetadata (domain_id, kind, content) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, content := range contents {
		if content == "" {
			continue
		}
		if _, err := stmt.Exec(zoneID, kind, content); err != nil {
			return fmt.Errorf("insert metadata %s: %w", kind, err)
		}
	}
	return nil
}

// UpsertZone replaces the zone's entire record set in a single
// transaction. Safer than partial updates: the operator-facing flow
// always sends the full desired state, and we never leave PowerDNS in
// a half-applied state mid-push.
func (c *Client) UpsertZone(name string, records []Record) (int64, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back on early return

	zoneID, err := c.upsertZoneCore(tx, name, records)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return zoneID, nil
}

// UpsertZoneWithMeta is UpsertZone + metadata, in the same txn.
func (c *Client) UpsertZoneWithMeta(name string, records []Record, opts UpsertZoneOptions) (int64, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back on early return

	zoneID, err := c.upsertZoneCore(tx, name, records)
	if err != nil {
		return 0, err
	}

	if err := c.setZoneMetadata(tx, zoneID, "ALLOW-AXFR-FROM", opts.AllowAXFRFrom); err != nil {
		return 0, err
	}
	if err := c.setZoneMetadata(tx, zoneID, "ALSO-NOTIFY", opts.AlsoNotify); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return zoneID, nil
}

// DeleteZone removes the zone and all its records. Idempotent — no
// error if the zone isn't there.
func (c *Client) DeleteZone(name string) error {
	_, err := c.db.Exec(`DELETE FROM domains WHERE name = ?`, name)
	return err
}
