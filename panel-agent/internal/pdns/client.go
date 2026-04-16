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
	dsn := fmt.Sprintf("%s:%s@tcp(127.0.0.1:3306)/%s?parseTime=true&charset=utf8mb4",
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

	// Find or create the zone row. PowerDNS's unique key is `name`.
	var zoneID int64
	err = tx.QueryRow(`SELECT id FROM domains WHERE name = ?`, name).Scan(&zoneID)
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
