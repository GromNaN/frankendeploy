package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
)

// IsLaravelProject reports whether the directory is a Laravel application.
// laravel/framework in composer.json is the canonical marker; artisan plus
// bootstrap/app.php (Laravel 11+) is the fallback when composer.json is
// missing or unreadable.
func (s *Scanner) IsLaravelProject() bool {
	if composer, err := s.ParseComposer(); err == nil && composer.HasLaravel {
		return true
	}
	return s.fileExists("artisan") && s.fileExists(filepath.Join("bootstrap", "app.php"))
}

// fileExists reports whether a path relative to the project root exists.
func (s *Scanner) fileExists(rel string) bool {
	_, err := os.Stat(filepath.Join(s.projectPath, rel))
	return err == nil
}

// GetLaravelEnv reads the Laravel environment: .env first, falling back to
// .env.example (Laravel gitignores .env, so a fresh clone only ships the
// example).
func (s *Scanner) GetLaravelEnv() (map[string]string, error) {
	if env, err := s.GetEnvFile(".env"); err == nil {
		return env, nil
	}
	return s.GetEnvFile(".env.example")
}

// DetectLaravelDatabase detects the database configuration from a Laravel
// project. It reads DATABASE_URL first, then the idiomatic DB_CONNECTION
// form. Unlike Symfony it does not default to PostgreSQL when nothing is
// configured: a Laravel app may legitimately have no database.
func (s *Scanner) DetectLaravelDatabase() (*config.DatabaseConfig, []string, error) {
	env, err := s.GetLaravelEnv()
	if err != nil {
		return nil, nil, nil
	}

	// DATABASE_URL wins when set: it is the single source of truth Laravel
	// reads through the connection's "url" key.
	if dbURL := env["DATABASE_URL"]; dbURL != "" {
		driver, version := parseDBURL(dbURL)
		if driver != "" {
			dbConfig := &config.DatabaseConfig{Driver: driver, Version: version}
			if driver == "sqlite" {
				dbConfig.Path = extractSQLitePath(dbURL)
			} else {
				managed := true
				dbConfig.Managed = &managed
			}
			return dbConfig, nil, nil
		}
	}

	connection := strings.ToLower(strings.TrimSpace(env["DB_CONNECTION"]))
	switch connection {
	case "":
		return nil, nil, nil
	case "sqlite":
		path := env["DB_DATABASE"]
		if path == "" {
			path = "database/database.sqlite"
		}
		return &config.DatabaseConfig{Driver: "sqlite", Version: "3", Path: path}, nil, nil
	case "mongodb":
		// DB_CONNECTION=mongodb marks mongodb/laravel-mongodb: no SQL driver.
		return nil, []string{"DB_CONNECTION=mongodb detected: MongoDB is configured through the mongodb service, not the database section"}, nil
	case "sqlsrv":
		return nil, []string{"DB_CONNECTION=sqlsrv is not supported: no SQL Server image is provided, set an external DATABASE_URL instead"}, nil
	case "pgsql", "postgres", "postgresql", "mysql", "mariadb":
		driver := config.NormalizeDBDriver(connection)
		managed := true
		return &config.DatabaseConfig{
			Driver:  driver,
			Version: getDefaultVersion(driver),
			Name:    env["DB_DATABASE"],
			Managed: &managed,
		}, nil, nil
	default:
		return nil, []string{fmt.Sprintf("unknown DB_CONNECTION %q: no managed database will be provisioned", connection)}, nil
	}
}

// DetectLaravelQueue reports the Laravel queue connection to consume.
// Laravel ships config/queue.php in every skeleton, so detection is based on
// the QUEUE_CONNECTION environment value ("sync" runs jobs inline) and on
// laravel/horizon.
func (s *Scanner) DetectLaravelQueue() (bool, string) {
	env, _ := s.GetLaravelEnv()
	connection := strings.ToLower(strings.TrimSpace(env["QUEUE_CONNECTION"]))
	if connection != "" && connection != "sync" {
		return true, connection
	}
	if composer, err := s.ParseComposer(); err == nil && composer.HasPackage("laravel/horizon") {
		return true, "redis"
	}
	return false, ""
}

// HasLaravelScheduler reports whether the Laravel scheduler defines tasks.
// Laravel 11 wires it in bootstrap/app.php (->withSchedule) or
// routes/console.php; older apps use app/Console/Kernel.php.
func (s *Scanner) HasLaravelScheduler() bool {
	if data, err := os.ReadFile(filepath.Join(s.projectPath, "bootstrap", "app.php")); err == nil {
		if strings.Contains(string(data), "withSchedule") {
			return true
		}
	}
	if data, err := os.ReadFile(filepath.Join(s.projectPath, "routes", "console.php")); err == nil {
		if strings.Contains(string(data), "Schedule::") {
			return true
		}
	}
	if data, err := os.ReadFile(filepath.Join(s.projectPath, "app", "Console", "Kernel.php")); err == nil {
		if strings.Contains(string(data), "$schedule") {
			return true
		}
	}
	return false
}
