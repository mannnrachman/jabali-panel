// Package db wires the panel's MariaDB connection and migration runner.
//
// We accept DSNs in two shapes to make config painless:
//
//  1. Go mysql-driver native form:
//     user:pass@tcp(host:3306)/db?parseTime=true&charset=utf8mb4&loc=UTC
//
//  2. URL form (as typically written in .env files):
//     mysql://user:pass@host:3306/db?parseTime=true&charset=utf8mb4&loc=UTC
//
// ToDriverDSN normalises (2) → (1); (1) passes through unchanged.
package db

import (
	"fmt"
	"net/url"
	"strings"
)

// ToDriverDSN returns a DSN usable by github.com/go-sql-driver/mysql.
// If s is already in native driver form, it is returned as-is.
func ToDriverDSN(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty DSN")
	}
	if !strings.HasPrefix(s, "mysql://") && !strings.HasPrefix(s, "mariadb://") {
		// Already native driver form (or something the driver can parse).
		return s, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}

	userinfo := ""
	if u.User != nil {
		userinfo = u.User.Username()
		if p, ok := u.User.Password(); ok {
			userinfo += ":" + p
		}
		userinfo += "@"
	}

	host := u.Host
	if host == "" {
		return "", fmt.Errorf("DSN missing host: %q", s)
	}

	path := strings.TrimPrefix(u.Path, "/")
	if path == "" {
		return "", fmt.Errorf("DSN missing database name: %q", s)
	}

	out := fmt.Sprintf("%stcp(%s)/%s", userinfo, host, path)
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, nil
}
