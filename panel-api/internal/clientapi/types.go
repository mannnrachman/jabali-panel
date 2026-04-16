package clientapi

import (
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// --- Domain types ---

type CreateDomainRequest struct {
	Name    string `json:"name"`
	UserID  string `json:"user_id"`
	DocRoot string `json:"doc_root"`
}

type UpdateDomainRequest struct {
	IsEnabled             *bool   `json:"is_enabled,omitempty"`
	NginxCustomDirectives *string `json:"nginx_custom_directives,omitempty"`
}

type DomainResponse struct {
	ID                     string     `json:"id"`
	UserID                 string     `json:"user_id"`
	Name                   string     `json:"name"`
	DocRoot                string     `json:"doc_root"`
	IsEnabled              bool       `json:"is_enabled"`
	NginxCustomDirectives  *string    `json:"nginx_custom_directives"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	ProvisionWarning       string     `json:"provision_warning,omitempty"`
}

type ListDomainsResponse struct {
	Data     []models.Domain `json:"data"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// --- User types ---

type CreateUserRequest struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	NameFirst string `json:"name_first"`
	NameLast  string `json:"name_last"`
	IsAdmin   bool   `json:"is_admin"`
}

type UpdateUserRequest struct {
	Email           *string `json:"email,omitempty"`
	NameFirst       *string `json:"name_first,omitempty"`
	NameLast        *string `json:"name_last,omitempty"`
	Password        *string `json:"password,omitempty"`
	CurrentPassword *string `json:"current_password,omitempty"`
	IsAdmin         *bool   `json:"is_admin,omitempty"`
}

type UserResponse struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	NameFirst    string `json:"name_first"`
	NameLast     string `json:"name_last"`
	IsAdmin      bool   `json:"is_admin"`
	PasswordHash string `json:"password_hash"`
}

type ListUsersResponse struct {
	Data     []models.User `json:"data"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// --- Package types ---

type CreatePackageRequest struct {
	Name             string `json:"name"`
	DiskQuotaMB      uint32 `json:"disk_quota_mb"`
	BandwidthQuotaMB uint32 `json:"bandwidth_quota_mb"`
	MaxDomains       uint32 `json:"max_domains"`
	MaxEmailAccounts uint32 `json:"max_email_accounts"`
	MaxDatabases     uint32 `json:"max_databases"`
	MaxFTPAccounts   uint32 `json:"max_ftp_accounts"`
	SSHEnabled       bool   `json:"ssh_enabled"`
	CGIEnabled       bool   `json:"cgi_enabled"`
}

type UpdatePackageRequest struct {
	Name             *string `json:"name,omitempty"`
	DiskQuotaMB      *uint32 `json:"disk_quota_mb,omitempty"`
	BandwidthQuotaMB *uint32 `json:"bandwidth_quota_mb,omitempty"`
	MaxDomains       *uint32 `json:"max_domains,omitempty"`
	MaxEmailAccounts *uint32 `json:"max_email_accounts,omitempty"`
	MaxDatabases     *uint32 `json:"max_databases,omitempty"`
	MaxFTPAccounts   *uint32 `json:"max_ftp_accounts,omitempty"`
	SSHEnabled       *bool   `json:"ssh_enabled,omitempty"`
	CGIEnabled       *bool   `json:"cgi_enabled,omitempty"`
}

type PackageResponse struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	DiskQuotaMB      uint32    `json:"disk_quota_mb"`
	BandwidthQuotaMB uint32    `json:"bandwidth_quota_mb"`
	MaxDomains       uint32    `json:"max_domains"`
	MaxEmailAccounts uint32    `json:"max_email_accounts"`
	MaxDatabases     uint32    `json:"max_databases"`
	MaxFTPAccounts   uint32    `json:"max_ftp_accounts"`
	SSHEnabled       bool      `json:"ssh_enabled"`
	CGIEnabled       bool      `json:"cgi_enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ListPackagesResponse struct {
	Data     []models.HostingPackage `json:"data"`
	Total    int64                   `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
}

// --- Error response ---

type ErrorResponse struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}
