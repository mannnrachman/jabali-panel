package models

import (
	"time"

	"gorm.io/gorm"
)

// Domain represents a hosted domain bound to a user account. Each domain
// gets an nginx vhost config managed by the agent and a document root
// under the user's home directory.
type Domain struct {
	ID     string `gorm:"type:char(26);primaryKey" json:"id"`
	UserID string `gorm:"type:char(26);not null;index:ix_domains_user_id" json:"user_id"`

	// Name is the fully qualified domain name (e.g. "example.com").
	// Unique across the entire panel — two users can't host the same domain.
	Name string `gorm:"type:varchar(255);uniqueIndex:ux_domains_name;not null" json:"name"`

	// DocRoot is the filesystem path served by nginx. Defaults to
	// /home/<username>/public_html/<domain> at creation time.
	DocRoot string `gorm:"type:varchar(512);not null;default:''" json:"doc_root"`

	// IsEnabled controls whether the nginx vhost symlink exists in
	// sites-enabled. Disabled domains still have their config on disk.
	IsEnabled bool `gorm:"type:tinyint(1);not null;default:1" json:"is_enabled"`

	// NginxCustomDirectives holds operator-provided nginx config snippets
	// injected into the server block. Validated before save — dangerous
	// directives (proxy_pass, lua_*) are rejected.
	NginxCustomDirectives *string `gorm:"type:text" json:"nginx_custom_directives,omitempty"`

	CreatedAt time.Time      `gorm:"type:datetime(6);not null" json:"created_at"`
	UpdatedAt time.Time      `gorm:"type:datetime(6);not null" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"type:datetime(6);index:ix_domains_deleted_at" json:"-"`
}

func (Domain) TableName() string { return "domains" }
