package generator

import "github.com/yoanbernabeu/frankendeploy/internal/config"

// FrameworkCommands holds the framework-specific console invocations that
// recur across the Dockerfile and entrypoint templates. Structural
// differences stay as {{ if .IsLaravel }} branches in the templates; the
// strings live here so they are defined once.
type FrameworkCommands struct {
	// PhpConsole is the console entrypoint: "php bin/console" or "php artisan".
	PhpConsole string
	// Optimize is the production warmup command: Symfony's cache:warmup or
	// Laravel's optimize.
	Optimize string
}

// CommandsFor returns the console commands for the given framework. The zero
// value and "symfony" both return Symfony commands.
func CommandsFor(fw config.Framework) FrameworkCommands {
	switch fw {
	case config.FrameworkLaravel:
		return FrameworkCommands{PhpConsole: "php artisan", Optimize: "php artisan optimize"}
	default:
		return FrameworkCommands{PhpConsole: "php bin/console", Optimize: "php bin/console cache:warmup --no-interaction"}
	}
}

// isLaravel reports whether the framework is Laravel.
func isLaravel(fw config.Framework) bool {
	return fw == config.FrameworkLaravel
}

// LaravelDBConnection maps a normalized database driver to the Laravel
// DB_CONNECTION name. Laravel ignores DATABASE_URL unless DB_CONNECTION
// selects a connection, so the value must match the driver.
func LaravelDBConnection(driver string) string {
	switch driver {
	case "pgsql":
		return "pgsql"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "sqlite":
		return "sqlite"
	default:
		return driver
	}
}
