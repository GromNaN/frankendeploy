package deploy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
	"github.com/yoanbernabeu/frankendeploy/internal/security"
	"github.com/yoanbernabeu/frankendeploy/internal/ssh"
)

// EnvRequirement defines a required environment variable
type EnvRequirement struct {
	Name         string
	Description  string
	CanGenerate  bool
	DefaultValue string
}

// EnvCheckResult holds the result of environment variable checking
type EnvCheckResult struct {
	Missing   []EnvRequirement
	Present   []string
	Generated map[string]string
}

// FrameworkEnvRequirements defines required variables by framework
var FrameworkEnvRequirements = map[config.Framework][]EnvRequirement{
	config.FrameworkSymfony: {
		{Name: "APP_SECRET", Description: "Symfony security secret", CanGenerate: true},
		{Name: "APP_ENV", Description: "Application environment", DefaultValue: "prod"},
	},
	config.FrameworkLaravel: {
		{Name: "APP_KEY", Description: "Laravel application encryption key", CanGenerate: true},
		// Laravel's convention is APP_ENV=production (not "prod"): packages
		// and App::environment('production') checks rely on the exact string.
		{Name: "APP_ENV", Description: "Application environment", DefaultValue: "production"},
		{Name: "APP_DEBUG", Description: "Debug mode", DefaultValue: "false"},
	},
}

// CheckEnvVars verifies that required environment variables are set on the server
func CheckEnvVars(ctx context.Context, client ssh.Executor, cfg *config.ProjectConfig, serverName string) (*EnvCheckResult, error) {
	result := &EnvCheckResult{
		Missing:   []EnvRequirement{},
		Present:   []string{},
		Generated: make(map[string]string),
	}

	// Read existing env variables
	existingVars, err := ReadEnvVars(ctx, client, cfg.Name)
	if err != nil {
		return nil, err
	}

	// Select requirements by framework. An unknown framework yields no
	// requirements: injecting Symfony's APP_SECRET into a non-Symfony app
	// would be wrong.
	requirements := FrameworkEnvRequirements[cfg.EffectiveFramework()]

	// Check each required variable
	for _, req := range requirements {
		if value, exists := existingVars[req.Name]; exists && value != "" {
			result.Present = append(result.Present, req.Name)
		} else if req.DefaultValue != "" {
			// Has a default value, so it's optional
			result.Present = append(result.Present, req.Name)
		} else {
			result.Missing = append(result.Missing, req)
		}
	}

	// Check DATABASE_URL if needed
	if needsDatabaseURL(cfg) {
		if value, exists := existingVars["DATABASE_URL"]; exists && value != "" {
			result.Present = append(result.Present, "DATABASE_URL")
		} else {
			result.Missing = append(result.Missing, EnvRequirement{
				Name:        "DATABASE_URL",
				Description: "Database connection URL (required - database is not managed)",
				CanGenerate: false,
			})
		}
	}

	// MongoDB enabled but not managed: an external MONGODB_URI is required
	// (e.g. Atlas), as FrankenDeploy does not provision the container.
	if cfg.MongoDB.Enabled && !cfg.MongoDB.Managed {
		if value, exists := existingVars["MONGODB_URI"]; exists && value != "" {
			result.Present = append(result.Present, "MONGODB_URI")
		} else {
			result.Missing = append(result.Missing, EnvRequirement{
				Name:        "MONGODB_URI",
				Description: "MongoDB connection URL (required - mongodb is not managed)",
				CanGenerate: false,
			})
		}
	}

	return result, nil
}

// needsDatabaseURL determines if DATABASE_URL is required based on config
func needsDatabaseURL(cfg *config.ProjectConfig) bool {
	// No database configured
	if cfg.Database.Driver == "" {
		return false
	}

	// Managed mode: FrankenDeploy provides DATABASE_URL
	if cfg.Database.IsManaged() {
		return false
	}

	// SQLite: no DATABASE_URL needed (file-based database)
	if cfg.Database.Driver == "sqlite" || cfg.Database.Driver == "pdo_sqlite" {
		return false
	}

	// External database: DATABASE_URL required
	return true
}

// LaravelDatabaseEnvVars returns the "KEY=value" pairs that connect a Laravel
// app to a managed database container. Laravel's config/database.php reads
// DB_CONNECTION plus DB_HOST/DB_PORT/DB_DATABASE/DB_USERNAME/DB_PASSWORD; it
// does not read DATABASE_URL, so the Symfony-style URL alone is not enough.
//
// Returns nil for a non-Laravel app, for a non-managed database (the user's
// own .env is authoritative and must not be overridden), or when the URL
// cannot be parsed. Values are shell-escaped for direct use in a docker run.
func LaravelDatabaseEnvVars(cfg *config.ProjectConfig, databaseURL string) []string {
	if cfg.EffectiveFramework() != config.FrameworkLaravel {
		return nil
	}
	if !cfg.Database.IsManaged() || databaseURL == "" {
		return nil
	}

	u, err := url.Parse(databaseURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}

	connection := laravelConnectionFromScheme(u.Scheme)
	if connection == "" {
		return nil
	}

	password, _ := u.User.Password()
	vars := []string{
		"DB_CONNECTION=" + connection,
		"DB_HOST=" + security.ShellEscape(u.Hostname()),
	}
	if port := u.Port(); port != "" {
		vars = append(vars, "DB_PORT="+security.ShellEscape(port))
	}
	if dbName := strings.TrimPrefix(u.Path, "/"); dbName != "" {
		vars = append(vars, "DB_DATABASE="+security.ShellEscape(dbName))
	}
	if user := u.User.Username(); user != "" {
		vars = append(vars, "DB_USERNAME="+security.ShellEscape(user))
	}
	if password != "" {
		vars = append(vars, "DB_PASSWORD="+security.ShellEscape(password))
	}

	return vars
}

// laravelConnectionFromScheme maps a database URL scheme to the Laravel
// DB_CONNECTION name. Returns "" for an unknown scheme.
func laravelConnectionFromScheme(scheme string) string {
	switch scheme {
	case "postgres", "postgresql", "pgsql":
		return "pgsql"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "sqlite":
		return "sqlite"
	default:
		return ""
	}
}

// GenerateSymfonySecret generates a secure 64-character hex secret
func GenerateSymfonySecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateLaravelAppKey generates a Laravel APP_KEY: "base64:" followed by the
// base64 encoding of 32 random bytes, the exact format `php artisan
// key:generate` produces.
func GenerateLaravelAppKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return "base64:" + base64.StdEncoding.EncodeToString(bytes), nil
}

// GenerateMissingSecrets generates values for variables that can be auto-generated
func GenerateMissingSecrets(missing []EnvRequirement) (map[string]string, error) {
	generated := make(map[string]string)

	for _, req := range missing {
		if !req.CanGenerate {
			continue
		}

		switch req.Name {
		case "APP_SECRET":
			secret, err := GenerateSymfonySecret()
			if err != nil {
				return nil, fmt.Errorf("failed to generate APP_SECRET: %w", err)
			}
			generated["APP_SECRET"] = secret
		case "APP_KEY":
			key, err := GenerateLaravelAppKey()
			if err != nil {
				return nil, fmt.Errorf("failed to generate APP_KEY: %w", err)
			}
			generated["APP_KEY"] = key
		}
	}

	return generated, nil
}

// SaveGeneratedSecrets writes generated secrets to the server's .env.local file
func SaveGeneratedSecrets(ctx context.Context, client ssh.Executor, appName string, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}

	// Read existing env file and merge new secrets
	existingVars, err := ReadEnvVars(ctx, client, appName)
	if err != nil {
		return err
	}
	for key, value := range secrets {
		existingVars[key] = value
	}

	return WriteEnvVars(ctx, client, appName, existingVars)
}

// FormatEnvCheckError formats an error message with commands to run
func FormatEnvCheckError(missing []EnvRequirement, serverName string) string {
	var sb strings.Builder

	sb.WriteString("Missing required environment variables:\n\n")

	for _, req := range missing {
		sb.WriteString(fmt.Sprintf("   %s (%s)\n", req.Name, req.Description))
	}

	sb.WriteString("\nRun the following commands to configure them:\n\n")

	for _, req := range missing {
		switch req.Name {
		case "APP_SECRET":
			sb.WriteString(fmt.Sprintf("   frankendeploy env set %s APP_SECRET=$(openssl rand -hex 32)\n", serverName))
		case "APP_KEY":
			sb.WriteString(fmt.Sprintf("   frankendeploy env set %s APP_KEY=\"base64:$(openssl rand -base64 32)\"\n", serverName))
		case "DATABASE_URL":
			sb.WriteString(fmt.Sprintf("   frankendeploy env set %s DATABASE_URL=\"postgresql://user:pass@host:5432/db\"\n", serverName))
		case "MONGODB_URI":
			sb.WriteString(fmt.Sprintf("   frankendeploy env set %s MONGODB_URI=\"mongodb+srv://user:pass@cluster0.mongodb.net/db\"\n", serverName))
		default:
			sb.WriteString(fmt.Sprintf("   frankendeploy env set %s %s=\"<value>\"\n", serverName, req.Name))
		}
	}

	sb.WriteString(fmt.Sprintf("\nThen run 'frankendeploy deploy %s' again.\n", serverName))
	sb.WriteString("\nOr use --force to skip this check (not recommended for production)")

	return sb.String()
}
