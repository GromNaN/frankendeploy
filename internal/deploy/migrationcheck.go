package deploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
	"github.com/yoanbernabeu/frankendeploy/internal/constants"
	"github.com/yoanbernabeu/frankendeploy/internal/ssh"
)

// migrationLayout describes where a framework keeps migrations and models and
// how to create/run migrations, for the migration-state check and warning.
type migrationLayout struct {
	MigrationsDir   string // find target for migration files
	ModelsDir       string // find target for model/entity files
	ModelsLabel     string // human label for the models dir
	MigrationsLabel string // human label for the migrations dir
	MakeCommand     string // command shown in the warning to create a migration
	RunCommand      string // command shown in the warning to run migrations
	AddPath         string // git add target shown in the warning
}

// layoutFor returns the migration layout for the given framework (zero value
// and unknown values fall back to Symfony).
func layoutFor(fw config.Framework) migrationLayout {
	if fw == config.FrameworkLaravel {
		return migrationLayout{
			MigrationsDir:   "/app/database/migrations",
			ModelsDir:       "/app/app/Models",
			ModelsLabel:     "app/Models/",
			MigrationsLabel: "database/migrations/",
			MakeCommand:     "php artisan make:migration <name>",
			RunCommand:      "php artisan migrate",
			AddPath:         "database/migrations/",
		}
	}
	return migrationLayout{
		MigrationsDir:   "/app/migrations",
		ModelsDir:       "/app/src/Entity",
		ModelsLabel:     "src/Entity/",
		MigrationsLabel: "migrations/",
		MakeCommand:     "php bin/console make:migration",
		RunCommand:      "php bin/console doctrine:migrations:migrate",
		AddPath:         "migrations/",
	}
}

// MigrationCheckResult holds the result of migration directory check
type MigrationCheckResult struct {
	MigrationFilesCount int
	EntityFilesCount    int
	HasPotentialProblem bool
}

// CheckMigrationState checks if migrations exist when models are present.
// It runs docker exec commands to count files in the framework's migrations
// and models directories.
func CheckMigrationState(ctx context.Context, client ssh.Executor, containerName string, fw config.Framework) (*MigrationCheckResult, error) {
	layout := layoutFor(fw)
	result := &MigrationCheckResult{}

	// Count migration files
	migrationCmd := fmt.Sprintf(
		"docker exec %s sh -c 'find %s -name \"*.php\" -type f 2>/dev/null | wc -l'",
		containerName, layout.MigrationsDir,
	)
	migrationResult, err := client.Exec(ctx, migrationCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to check migrations: %w", err)
	}
	result.MigrationFilesCount = parseCount(migrationResult.Stdout)

	// Count model/entity files
	entityCmd := fmt.Sprintf(
		"docker exec %s sh -c 'find %s -name \"*.php\" -type f 2>/dev/null | wc -l'",
		containerName, layout.ModelsDir,
	)
	entityResult, err := client.Exec(ctx, entityCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to check entities: %w", err)
	}
	result.EntityFilesCount = parseCount(entityResult.Stdout)

	// Determine if there's a potential problem
	// Problem: entities exist (> 0) but no migrations (== 0)
	result.HasPotentialProblem = result.EntityFilesCount > 0 && result.MigrationFilesCount == 0

	return result, nil
}

// parseCount extracts an integer from command output
func parseCount(output string) int {
	trimmed := strings.TrimSpace(output)
	count, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0
	}
	return count
}

// HasMigrationWarningBeenShown checks if the warning marker exists on the server
func HasMigrationWarningBeenShown(ctx context.Context, client ssh.Executor, appName string) bool {
	markerPath := getMigrationWarningMarkerPath(appName)
	cmd := fmt.Sprintf("test -f %s && echo 'exists'", markerPath)
	result, err := client.Exec(ctx, cmd)
	if err != nil {
		return false
	}
	return strings.TrimSpace(result.Stdout) == "exists"
}

// MarkMigrationWarningShown creates the warning marker file on the server
func MarkMigrationWarningShown(ctx context.Context, client ssh.Executor, appName string) error {
	markerPath := getMigrationWarningMarkerPath(appName)
	// Ensure directory exists and create marker file
	cmd := fmt.Sprintf("mkdir -p $(dirname %s) && touch %s", markerPath, markerPath)
	_, err := client.Exec(ctx, cmd)
	return err
}

// ClearMigrationWarningMarker removes the marker when the problem is resolved
func ClearMigrationWarningMarker(ctx context.Context, client ssh.Executor, appName string) error {
	markerPath := getMigrationWarningMarkerPath(appName)
	cmd := fmt.Sprintf("rm -f %s", markerPath)
	_, err := client.Exec(ctx, cmd)
	return err
}

// getMigrationWarningMarkerPath returns the path to the warning marker file
func getMigrationWarningMarkerPath(appName string) string {
	return filepath.Join(constants.AppSharedPath(appName), ".migration_warning_shown")
}

// FormatMigrationWarning formats the warning message for display
func FormatMigrationWarning(result *MigrationCheckResult, fw config.Framework) string {
	layout := layoutFor(fw)
	var sb strings.Builder

	sb.WriteString("Warning: No database migrations found but models exist!\n\n")
	sb.WriteString(fmt.Sprintf("   Models found: %d files in %s\n", result.EntityFilesCount, layout.ModelsLabel))
	sb.WriteString(fmt.Sprintf("   Migrations:   %d files in %s\n", result.MigrationFilesCount, layout.MigrationsLabel))
	sb.WriteString("\n")
	sb.WriteString("   This may cause 'no such table' errors at runtime.\n\n")
	sb.WriteString("   To fix this, run locally:\n")
	sb.WriteString("      " + layout.MakeCommand + "\n")
	sb.WriteString("      " + layout.RunCommand + "\n")
	sb.WriteString("      git add " + layout.AddPath + "\n")
	sb.WriteString("      git commit -m \"Add database migrations\"\n\n")
	sb.WriteString("   Then redeploy your application.\n")

	return sb.String()
}

// HasMigrationHook checks if the hooks list contains a migration command for
// either framework: Doctrine (Symfony) or artisan migrate (Laravel, including
// migrate --force and the migrate:* variants).
func HasMigrationHook(hooks []string) bool {
	for _, hook := range hooks {
		if strings.Contains(hook, "doctrine:migrations:migrate") ||
			strings.Contains(hook, "d:m:m") ||
			strings.Contains(hook, "artisan migrate") {
			return true
		}
	}
	return false
}

// HookError is returned when a deployment hook fails. MigrationAttempted
// tells whether a migration hook ran before or as the failing one: only then
// can the schema be partially migrated.
type HookError struct {
	Hook               string
	MigrationAttempted bool
	Err                error
}

func (e *HookError) Error() string {
	return fmt.Sprintf("hook '%s' failed: %v", e.Hook, e.Err)
}

func (e *HookError) Unwrap() error {
	return e.Err
}

// NewHookError builds the error for hooks[failed], computing whether a
// migration ran up to that point.
func NewHookError(hooks []string, failed int, err error) *HookError {
	return &HookError{
		Hook:               hooks[failed],
		MigrationAttempted: HasMigrationHook(hooks[:failed+1]),
		Err:                err,
	}
}

// migrationMayHaveRun reports whether a pre_deploy failure may have left a
// partially migrated schema. Unknown failures are treated as such.
func migrationMayHaveRun(err error) bool {
	var hookErr *HookError
	if errors.As(err, &hookErr) {
		return hookErr.MigrationAttempted
	}
	return true
}
