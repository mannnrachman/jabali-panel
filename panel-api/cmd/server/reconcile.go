package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
)

func newReconcileCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile nginx vhosts from database",
		Long:  "Force regenerate nginx vhosts configuration from the database via the admin API.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), sharedCfg.Agent.Timeout)
			defer cancel()

			token, err := mintCLIToken(ctx)
			if err != nil {
				return fmt.Errorf("mint token: %w", err)
			}

			return runReconcile(ctx, sharedCfg, token, force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force full regeneration of all domains")

	return cmd
}

func runReconcile(ctx context.Context, cfg *config.Config, token string, force bool) error {
	payload := map[string]interface{}{
		"scope": "all",
		"force": force,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// Build URL from config
	scheme := "http"
	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("parse server addr: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("%s://%s:%s/api/v1/admin/reconcile", scheme, host, port)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		msg := "unknown error"
		if m, ok := result["message"].(string); ok {
			msg = m
		}
		return fmt.Errorf("reconciliation failed: %s (HTTP %d)", msg, resp.StatusCode)
	}

	fmt.Println("Reconciliation complete")
	if status, ok := result["status"].(string); ok {
		fmt.Printf("Status: %s\n", status)
	}
	if msg, ok := result["message"].(string); ok {
		fmt.Printf("Message: %s\n", msg)
	}

	return nil
}

func newReconcilerPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause the reconciler (for SSO key rotation)",
		Long:  "Pause the reconciler to prevent it from running during SSO key rotation.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), sharedCfg.Agent.Timeout)
			defer cancel()

			token, err := mintCLIToken(ctx)
			if err != nil {
				return fmt.Errorf("mint token: %w", err)
			}

			return runReconcilerPause(ctx, sharedCfg, token)
		},
	}
}

func newReconcilerResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume the reconciler",
		Long:  "Resume the reconciler after a pause.",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), sharedCfg.Agent.Timeout)
			defer cancel()

			token, err := mintCLIToken(ctx)
			if err != nil {
				return fmt.Errorf("mint token: %w", err)
			}

			return runReconcilerResume(ctx, sharedCfg, token)
		},
	}
}

func runReconcilerPause(ctx context.Context, cfg *config.Config, token string) error {
	payload := map[string]interface{}{
		"action": "pause",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	scheme := "http"
	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("parse server addr: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("%s://%s:%s/api/v1/admin/reconciler/pause", scheme, host, port)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pause failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	fmt.Println("Reconciler paused")
	return nil
}

func runReconcilerResume(ctx context.Context, cfg *config.Config, token string) error {
	payload := map[string]interface{}{
		"action": "resume",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	scheme := "http"
	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("parse server addr: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("%s://%s:%s/api/v1/admin/reconciler/resume", scheme, host, port)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resume failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	fmt.Println("Reconciler resumed")
	return nil
}
