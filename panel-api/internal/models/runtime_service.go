package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// RuntimeType enumerates the supported application runtimes.
// Each value maps to a different nginx backend strategy and
// process-management approach on the host.
const (
	RuntimePHP    = "php"    // PHP-FPM via fastcgi_pass (existing path)
	RuntimeNodeJS = "nodejs" // Node.js process via proxy_pass
	RuntimePython = "python" // Python (gunicorn/uvicorn) via proxy_pass
	RuntimeGo     = "go"     // Go binary via proxy_pass
	RuntimeDocker = "docker" // Docker container via proxy_pass
	RuntimeStatic = "static" // Static files only (no backend)
)

// ValidRuntimeTypes is the closed set of runtime_type values the API
// accepts. Used for validation at the handler layer. Ordering matches
// the UI dropdown.
var ValidRuntimeTypes = []string{
	RuntimePHP,
	RuntimeNodeJS,
	RuntimePython,
	RuntimeGo,
	RuntimeDocker,
	RuntimeStatic,
}

// IsValidRuntimeType reports whether rt is a recognised runtime type.
func IsValidRuntimeType(rt string) bool {
	for _, v := range ValidRuntimeTypes {
		if v == rt {
			return true
		}
	}
	return false
}

// RuntimeNeedsProxy reports whether the given runtime type requires
// an nginx reverse-proxy (proxy_pass) block instead of PHP-FPM
// (fastcgi_pass). Static and PHP are the two non-proxy runtimes.
func RuntimeNeedsProxy(rt string) bool {
	switch rt {
	case RuntimePHP, RuntimeStatic, "":
		return false
	default:
		return true
	}
}

// Runtime service lifecycle states. These are the canonical status
// values persisted in runtime_services.status and interpreted by the
// reconciler, API, and UI. Keep this list in sync with the comment on
// migration 000149 and the UI's status renderer.
const (
	RuntimeStatusPending   = "pending"   // needs (re)deploy
	RuntimeStatusDeploying  = "deploying" // deploy in flight (npm install / build)
	RuntimeStatusRunning    = "running"   // unit enabled + started
	RuntimeStatusStopped    = "stopped"   // unit installed but domain disabled
	RuntimeStatusFailed     = "failed"    // deploy/apply error or crashed unit
)

// RuntimeService represents a managed application process (Node.js,
// Python, Go, or Docker) bound to exactly one domain. The reconciler
// reads runtime_services and converges the host state — systemd
// units, nginx vhosts, port allocation — to match.
//
// For PHP domains, no RuntimeService row exists; the existing PHPPool
// model handles that path. RuntimeService is exclusively for non-PHP
// runtimes introduced by the multi-runtime hosting extension.
type RuntimeService struct {
	ID          string  `gorm:"type:char(26);primaryKey" json:"id"`
	DomainID    string  `gorm:"type:char(26);not null;uniqueIndex:ux_runtime_services_domain" json:"domain_id"`
	UserID      string  `gorm:"type:char(26);not null;index:ix_runtime_services_user" json:"user_id"`
	Runtime     string  `gorm:"type:varchar(16);not null" json:"runtime"`
	Version     string  `gorm:"type:varchar(16);not null;default:''" json:"version"`
	EntryPoint  string  `gorm:"type:varchar(512);not null" json:"entry_point"`
	ListenPort  uint32  `gorm:"type:int unsigned;not null;uniqueIndex:ux_runtime_services_port" json:"listen_port"`
	EnvVars     EnvVars `gorm:"type:json" json:"env_vars,omitempty"`
	Status      string  `gorm:"type:varchar(16);not null;default:'pending';index:ix_runtime_services_status" json:"status"`
	LastError   *string `gorm:"type:text" json:"last_error,omitempty"`
	PidFile     string  `gorm:"type:varchar(255);not null;default:''" json:"pid_file"`
	SystemdUnit string  `gorm:"type:varchar(255);not null;default:''" json:"systemd_unit"`
	CreatedAt   time.Time `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"type:datetime(6);not null" json:"updated_at"`
}

func (RuntimeService) TableName() string { return "runtime_services" }

// EnvVars implements driver.Valuer / sql.Scanner so GORM can persist
// the map to a JSON column. Follows the same pattern as PageRedirects
// and NginxRules on the Domain model.
type EnvVars map[string]string

func (e EnvVars) Value() (driver.Value, error) {
	if len(e) == 0 {
		return nil, nil
	}
	return json.Marshal(e)
}

func (e *EnvVars) Scan(src any) error {
	if src == nil {
		*e = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("EnvVars.Scan: unsupported type %T", src)
	}
	if len(b) == 0 {
		*e = nil
		return nil
	}
	return json.Unmarshal(b, e)
}
