package scanner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
)

const minimalLaravelComposer = `{"require": {"php": ">=8.2", "laravel/framework": "^11.0"}}`

// writeLaravelProject writes the minimal files that make a directory a
// Laravel project. env is written verbatim to .env when non-empty.
func writeLaravelProject(t *testing.T, dir, env string) {
	t.Helper()
	writeFile(t, dir, "composer.json", minimalLaravelComposer)
	writeFile(t, dir, "artisan", "#!/usr/bin/env php\n")
	writeFile(t, dir, "bootstrap/app.php", "<?php\nreturn Application::configure();\n")
	if env != "" {
		writeFile(t, dir, ".env", env)
	}
}

func TestIsLaravelProject_ComposerMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "composer.json", minimalLaravelComposer)

	if !New(dir).IsLaravelProject() {
		t.Error("laravel/framework in composer.json must mark the project as Laravel")
	}
}

func TestIsLaravelProject_ArtisanFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "artisan", "#!/usr/bin/env php\n")
	writeFile(t, dir, "bootstrap/app.php", "<?php\n")

	if !New(dir).IsLaravelProject() {
		t.Error("artisan + bootstrap/app.php must mark the project as Laravel")
	}
}

func TestIsLaravelProject_SymfonyIsNotLaravel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "composer.json", minimalSymfonyComposer)

	if New(dir).IsLaravelProject() {
		t.Error("a Symfony project must not be detected as Laravel")
	}
}

func TestIsSymfonyProject_LaravelIsNotSymfony(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "composer.json", `{"require": {"laravel/framework": "^11.0", "symfony/console": "^7.0"}}`)

	if New(dir).IsSymfonyProject() {
		t.Error("a Laravel project must not be detected as Symfony")
	}
}

func TestScan_LaravelDefaults(t *testing.T) {
	dir := t.TempDir()
	writeLaravelProject(t, dir, "DB_CONNECTION=pgsql\nDB_DATABASE=app\n")

	result, err := New(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Framework != config.FrameworkLaravel {
		t.Fatalf("Framework = %q, want laravel", result.Framework)
	}
	if result.IsSymfony {
		t.Error("IsSymfony must be false for a Laravel project")
	}

	cfg := New(dir).ToProjectConfig(result, "app")

	if cfg.Framework != config.FrameworkLaravel {
		t.Errorf("cfg.Framework = %q, want laravel", cfg.Framework)
	}
	if !cfg.Database.IsManaged() || cfg.Database.Driver != "pgsql" {
		t.Errorf("DB_CONNECTION=pgsql must yield a managed pgsql database, got %+v", cfg.Database)
	}

	wantDirs := []string{"storage/app", "storage/framework/cache", "storage/framework/sessions", "storage/logs"}
	if strings.Join(cfg.Deploy.SharedDirs, ",") != strings.Join(wantDirs, ",") {
		t.Errorf("shared dirs = %v, want %v", cfg.Deploy.SharedDirs, wantDirs)
	}
	if strings.Join(cfg.Deploy.SharedFiles, ",") != ".env" {
		t.Errorf("shared files = %v, want [.env]", cfg.Deploy.SharedFiles)
	}

	if len(cfg.Deploy.Hooks.PreDeploy) != 1 || !strings.Contains(cfg.Deploy.Hooks.PreDeploy[0], "php artisan migrate --force") {
		t.Errorf("expected the artisan migrate --force hook, got %v", cfg.Deploy.Hooks.PreDeploy)
	}
}

func TestScan_LaravelQueueAndScheduler(t *testing.T) {
	dir := t.TempDir()
	writeLaravelProject(t, dir, "DB_CONNECTION=sqlite\nQUEUE_CONNECTION=redis\n")
	writeFile(t, dir, "routes/console.php", "<?php\nSchedule::command('emails:send')->daily();\n")

	result, err := New(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !result.HasQueue || result.QueueConnection != "redis" {
		t.Errorf("expected a redis queue, got enabled=%v connection=%q", result.HasQueue, result.QueueConnection)
	}
	if !result.HasLaravelScheduler {
		t.Error("Schedule:: in routes/console.php must mark the scheduler as used")
	}

	cfg := New(dir).ToProjectConfig(result, "app")
	if !cfg.Queue.Enabled || cfg.Queue.Connection != "redis" {
		t.Errorf("queue config = %+v, want enabled redis", cfg.Queue)
	}
	if !cfg.Scheduler.Enabled {
		t.Error("scheduler config must be enabled")
	}
	if !contains(result.PHPExtensions, "redis") {
		t.Errorf("redis extension must be added for a redis queue, got %v", result.PHPExtensions)
	}
	if !contains(result.PHPExtensions, "pcntl") {
		t.Errorf("pcntl extension must be added for a queue worker, got %v", result.PHPExtensions)
	}
	// SQLite shares the database file, not the whole directory (which holds
	// database/migrations).
	if !contains(cfg.Deploy.SharedFiles, "database/database.sqlite") {
		t.Errorf("the SQLite file must be shared, got %v", cfg.Deploy.SharedFiles)
	}
	for _, d := range cfg.Deploy.SharedDirs {
		if strings.HasPrefix(d, "database") {
			t.Errorf("the database directory must not be shared, got %v", cfg.Deploy.SharedDirs)
		}
	}
}

func TestScan_LaravelNoQueueNoScheduler(t *testing.T) {
	dir := t.TempDir()
	writeLaravelProject(t, dir, "QUEUE_CONNECTION=sync\n")

	result, err := New(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.HasQueue {
		t.Error("QUEUE_CONNECTION=sync must not enable a worker")
	}

	cfg := New(dir).ToProjectConfig(result, "app")
	if cfg.Queue.Enabled || cfg.Scheduler.Enabled {
		t.Errorf("no worker or scheduler expected, got queue=%+v scheduler=%+v", cfg.Queue, cfg.Scheduler)
	}
	// No database configured: no migration hook either.
	if len(cfg.Deploy.Hooks.PreDeploy) != 0 {
		t.Errorf("no migration hook expected without a database, got %v", cfg.Deploy.Hooks.PreDeploy)
	}
}

func TestScan_RejectsUnknownFramework(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "composer.json", `{"require": {"drupal/core": "^10.0"}}`)

	_, err := New(dir).Scan()
	if err == nil {
		t.Fatal("Scan must reject a project that is neither Symfony nor Laravel")
	}
	if !strings.Contains(err.Error(), "Laravel") {
		t.Errorf("the error should mention Laravel, got: %v", err)
	}
}

func TestDetectLaravelDatabase(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		wantDriver  string
		wantVersion string
		wantPath    string
		wantManaged bool
		wantWarn    string
	}{
		{
			name:        "DATABASE_URL pgsql",
			env:         "DATABASE_URL=postgresql://app:x@db:5432/app?serverVersion=16\n",
			wantDriver:  "pgsql",
			wantVersion: "16",
			wantManaged: true,
		},
		{
			name:        "DB_CONNECTION pgsql",
			env:         "DB_CONNECTION=pgsql\nDB_DATABASE=app\n",
			wantDriver:  "pgsql",
			wantVersion: "16",
			wantManaged: true,
		},
		{
			name:        "DB_CONNECTION mysql",
			env:         "DB_CONNECTION=mysql\n",
			wantDriver:  "mysql",
			wantVersion: "8.0",
			wantManaged: true,
		},
		{
			name:       "DB_CONNECTION sqlite default path",
			env:        "DB_CONNECTION=sqlite\n",
			wantDriver: "sqlite",
			wantPath:   "database/database.sqlite",
		},
		{
			name:       "DB_CONNECTION sqlite custom path",
			env:        "DB_CONNECTION=sqlite\nDB_DATABASE=/data/app.sqlite\n",
			wantDriver: "sqlite",
			wantPath:   "/data/app.sqlite",
		},
		{
			name:     "DB_CONNECTION mongodb",
			env:      "DB_CONNECTION=mongodb\n",
			wantWarn: "mongodb",
		},
		{
			name:     "DB_CONNECTION sqlsrv",
			env:      "DB_CONNECTION=sqlsrv\n",
			wantWarn: "sqlsrv",
		},
		{
			name:     "unknown connection",
			env:      "DB_CONNECTION=oracle\n",
			wantWarn: "oracle",
		},
		{
			name: "nothing configured",
			env:  "APP_ENV=local\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, ".env", tt.env)

			db, warnings, err := New(dir).DetectLaravelDatabase()
			if err != nil {
				t.Fatalf("DetectLaravelDatabase: %v", err)
			}
			if tt.wantDriver == "" {
				if db != nil {
					t.Errorf("expected no database, got %+v", db)
				}
			} else {
				if db == nil {
					t.Fatalf("expected a %s database, got nil", tt.wantDriver)
				}
				if db.Driver != tt.wantDriver {
					t.Errorf("driver = %q, want %q", db.Driver, tt.wantDriver)
				}
				if tt.wantVersion != "" && db.Version != tt.wantVersion {
					t.Errorf("version = %q, want %q", db.Version, tt.wantVersion)
				}
				if tt.wantPath != "" && db.Path != tt.wantPath {
					t.Errorf("path = %q, want %q", db.Path, tt.wantPath)
				}
				if tt.wantManaged && (db.Managed == nil || !*db.Managed) {
					t.Errorf("expected a managed database, got %+v", db)
				}
			}
			if tt.wantWarn != "" {
				found := false
				for _, w := range warnings {
					if strings.Contains(w, tt.wantWarn) {
						found = true
					}
				}
				if !found {
					t.Errorf("expected a warning mentioning %q, got %v", tt.wantWarn, warnings)
				}
			}
		})
	}
}

func TestDetectLaravelQueue(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		composer string
		wantOn   bool
		wantConn string
	}{
		{
			name:     "database connection",
			env:      "QUEUE_CONNECTION=database\n",
			wantOn:   true,
			wantConn: "database",
		},
		{
			name:     "redis connection",
			env:      "QUEUE_CONNECTION=redis\n",
			wantOn:   true,
			wantConn: "redis",
		},
		{
			name:   "sync is not a worker",
			env:    "QUEUE_CONNECTION=sync\n",
			wantOn: false,
		},
		{
			name:   "unset",
			env:    "APP_ENV=local\n",
			wantOn: false,
		},
		{
			name:     "horizon implies redis",
			env:      "QUEUE_CONNECTION=sync\n",
			composer: `{"require": {"laravel/framework": "^11.0", "laravel/horizon": "^5.0"}}`,
			wantOn:   true,
			wantConn: "redis",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, ".env", tt.env)
			if tt.composer != "" {
				writeFile(t, dir, "composer.json", tt.composer)
			}

			enabled, connection := New(dir).DetectLaravelQueue()
			if enabled != tt.wantOn {
				t.Errorf("enabled = %v, want %v", enabled, tt.wantOn)
			}
			if connection != tt.wantConn {
				t.Errorf("connection = %q, want %q", connection, tt.wantConn)
			}
		})
	}
}

func TestGetLaravelEnv_FallsBackToExample(t *testing.T) {
	dir := t.TempDir()
	// .env is gitignored: a fresh clone only has .env.example.
	writeFile(t, dir, ".env.example", "DB_CONNECTION=pgsql\n")

	env, err := New(dir).GetLaravelEnv()
	if err != nil {
		t.Fatalf("GetLaravelEnv: %v", err)
	}
	if env["DB_CONNECTION"] != "pgsql" {
		t.Errorf("DB_CONNECTION = %q, want pgsql", env["DB_CONNECTION"])
	}
}

func TestHasLaravelScheduler_BootstrapApp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join("bootstrap", "app.php"),
		"<?php\n->withSchedule(function (Schedule $schedule) {\n})\n")

	if !New(dir).HasLaravelScheduler() {
		t.Error("withSchedule in bootstrap/app.php must mark the scheduler as used")
	}
}

func TestHasLaravelScheduler_None(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join("bootstrap", "app.php"), "<?php\nreturn Application::configure();\n")

	if New(dir).HasLaravelScheduler() {
		t.Error("no scheduled task must be detected")
	}
}
