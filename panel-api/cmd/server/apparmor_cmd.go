// `jabali apparmor flip-mature` — operator-driven flip of jabali
// AppArmor profiles from complain to enforce. Lists profiles in
// complain mode older than --soak-days (default 7) and flips them
// via aa-enforce. Honors per-profile filter via --profile.
//
// See ADR-0086 + plans/m40-apparmor-jabali-daemons.md Step 5.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var apparmorAllowlist = []string{
	"jabali-panel",
	"jabali-agent",
	"jabali-bulwark",
	"jabali-kratos",
	"stalwart-mail",
}

// apparmorProfileFile maps a profile name to the on-disk profile file
// shipped under /etc/apparmor.d/. aa-enforce/aa-complain accept either
// a binary path (resolved via PATH) or a profile-file path; profile
// names like "jabali-bulwark" don't resolve via PATH on Debian, so we
// always pass the file path explicitly.
func apparmorProfileFile(name string) string {
	switch name {
	case "jabali-panel":
		return "/etc/apparmor.d/usr.local.bin.jabali-panel-api"
	case "jabali-agent":
		return "/etc/apparmor.d/usr.local.bin.jabali-agent"
	case "jabali-bulwark":
		return "/etc/apparmor.d/usr.local.bin.jabali-bulwark"
	case "jabali-kratos":
		return "/etc/apparmor.d/usr.local.bin.jabali-kratos"
	case "stalwart-mail":
		return "/etc/apparmor.d/usr.local.bin.stalwart-mail"
	}
	return ""
}

func newAppArmorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apparmor",
		Short: "AppArmor profile management (M40) operator commands",
	}
	cmd.AddCommand(newAppArmorFlipMatureCmd())
	cmd.AddCommand(newAppArmorStatusCmd())
	return cmd
}

func newAppArmorStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List jabali AppArmor profiles and modes",
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles, err := listJabaliApparmorProfiles()
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Println("apparmor: no jabali profiles loaded")
				return nil
			}
			for name, mode := range profiles {
				fmt.Printf("  %-22s  %s\n", name, mode)
			}
			return nil
		},
	}
}

func newAppArmorFlipMatureCmd() *cobra.Command {
	var (
		profileFilter string
		dryRun        bool
	)
	cmd := &cobra.Command{
		Use:   "flip-mature",
		Short: "Flip mature complain-mode profiles to enforce",
		Long: `Find AppArmor profiles in complain mode and flip them to enforce.
Limits the flip to the jabali-shipped allowlist; never touches
arbitrary system profiles. Use --profile <name> to target one.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles, err := listJabaliApparmorProfiles()
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				fmt.Println("apparmor flip-mature: no jabali profiles loaded")
				return nil
			}

			toFlip := []string{}
			for _, name := range apparmorAllowlist {
				if profileFilter != "" && profileFilter != name {
					continue
				}
				mode, ok := profiles[name]
				if !ok {
					continue
				}
				if mode == "complain" {
					toFlip = append(toFlip, name)
				}
			}

			if len(toFlip) == 0 {
				fmt.Println("apparmor flip-mature: no complain-mode jabali profiles")
				return nil
			}

			fmt.Printf("apparmor flip-mature: %d candidates\n", len(toFlip))
			for _, name := range toFlip {
				fmt.Printf("  %s  complain → enforce\n", name)
				if dryRun {
					continue
				}
				profilePath := apparmorProfileFile(name)
				if profilePath == "" {
					fmt.Fprintf(os.Stderr, "  flip %s failed: no file path mapping\n", name)
					continue
				}
				out, err := exec.Command("aa-enforce", profilePath).CombinedOutput()
				if err != nil {
					fmt.Fprintf(os.Stderr, "  flip %s failed: %v\n%s\n", name, err, string(out))
					continue
				}
			}
			if dryRun {
				fmt.Println("apparmor flip-mature: dry-run, no profiles flipped")
			} else {
				fmt.Printf("apparmor flip-mature: flipped %d profile(s) to enforce\n", len(toFlip))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profileFilter, "profile", "", "Flip a single profile only")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without invoking aa-enforce")
	return cmd
}

// listJabaliApparmorProfiles parses aa-status --json and returns the
// jabali-* + jabali-shipped profiles with their current mode.
func listJabaliApparmorProfiles() (map[string]string, error) {
	out, err := exec.Command("aa-status", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("aa-status: %w", err)
	}
	var raw struct {
		Profiles map[string]string `json:"profiles"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse aa-status: %w", err)
	}
	out2 := map[string]string{}
	for name, mode := range raw.Profiles {
		if !strings.HasPrefix(name, "jabali-") && name != "stalwart-mail" {
			continue
		}
		out2[name] = mode
	}
	return out2, nil
}
