package cpanel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/cronvalidate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// CronImportResult is returned by ImportCron for restore-stage
// progress reporting + manifest update. Imported rows land disabled
// (Enabled=false); operator reviews + flips on after migration.
type CronImportResult struct {
	Created int
	Skipped []string // parse / allowlist failures with reason
}

// ImportCron walks each cPanel crontab file in the parsed tarball
// and inserts one cron_jobs row per parseable line. Lines that fail
// the cronvalidate command allowlist (cPanel users routinely run
// /usr/bin/php scripts at absolute paths or wget cron-trigger URLs;
// jabali's allowlist is wp / php only) are recorded in Skipped so
// the operator can manually re-enter them via the UI after
// migration.
//
// All inserted rows are Enabled=false — reconciler never schedules
// them until the operator reviews + flips. This is the safety
// gate: a malicious crontab in a compromised cPanel tarball can't
// land an active job by reaching restore.
//
// targetUserID + targetUsername are the destination jabali identity
// the restore stage created moments earlier. cronvalidate needs
// the username's owned-docroots list to validate `--path` args;
// for migration v1 we pass an empty list (rejects every wp/php
// command requiring a docroot path) and rely on the operator
// re-entering the cron after migration when the docroots are
// final. Trade-off: import skips most cPanel crons up front,
// avoids the false-positive of inserting a cron that points at a
// path that doesn't exist yet.
func ImportCron(ctx context.Context, repo repository.CronJobRepository, parsed *ParsedTarball, targetUserID string) (*CronImportResult, error) {
	if repo == nil {
		return nil, fmt.Errorf("ImportCron: repo nil")
	}
	if parsed == nil {
		return nil, fmt.Errorf("ImportCron: parsed nil")
	}
	if targetUserID == "" {
		return nil, fmt.Errorf("ImportCron: targetUserID empty")
	}
	res := &CronImportResult{}

	for _, cronPath := range parsed.CronFiles {
		f, err := os.Open(cronPath)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("open %s: %v", cronPath, err))
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 4096), 64*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			schedule, command, ok := splitCronLine(line)
			if !ok {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s:%d malformed", cronPath, lineNum))
				continue
			}
			// Schedule + command pass through the same validator
			// the REST handler runs. Empty docroots → strict mode
			// (any --path arg rejected). v1 trade-off captured in
			// the function comment.
			if vErr := cronvalidate.ValidateSchedule(schedule); vErr != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s:%d schedule: %s", cronPath, lineNum, vErr.Error()))
				continue
			}
			if _, vErr := cronvalidate.ValidateCommand(command, nil); vErr != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s:%d command: %s", cronPath, lineNum, vErr.Error()))
				continue
			}

			row := &models.CronJob{
				ID:        ids.NewULID(),
				UserID:    targetUserID,
				Name:      fmt.Sprintf("imported-%d", res.Created+1),
				Command:   command,
				Schedule:  schedule,
				Enabled:   false, // operator reviews + flips
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
			if err := repo.Create(ctx, row); err != nil {
				_ = f.Close()
				return res, fmt.Errorf("create cron_jobs row (line %d of %s): %w", lineNum, cronPath, err)
			}
			res.Created++
		}
		if err := scanner.Err(); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("scan %s: %v", cronPath, err))
		}
		_ = f.Close()
	}
	return res, nil
}

// splitCronLine cuts a standard 5-field crontab line into
// '<min hr dom mon dow>' + '<command>'. Returns (schedule, command,
// true) on success; (_, _, false) when the line has fewer than 6
// whitespace-separated fields.
//
// cPanel allows the @reboot / @daily / @hourly aliases too. We
// accept those by detecting a leading '@' and treating the first
// token as the entire schedule.
func splitCronLine(line string) (schedule, command string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", false
	}
	if strings.HasPrefix(fields[0], "@") {
		if len(fields) < 2 {
			return "", "", false
		}
		return fields[0], strings.Join(fields[1:], " "), true
	}
	if len(fields) < 6 {
		return "", "", false
	}
	return strings.Join(fields[:5], " "), strings.Join(fields[5:], " "), true
}
