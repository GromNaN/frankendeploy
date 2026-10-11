package generator

import (
	"strings"
	"testing"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
)

func laravelConfig() *config.ProjectConfig {
	managed := true
	return &config.ProjectConfig{
		Name:      "test-app",
		Framework: config.FrameworkLaravel,
		PHP:       config.PHPConfig{Version: "8.3", Extensions: []string{"intl", "opcache", "pdo_pgsql"}},
		Database:  config.DatabaseConfig{Driver: "pgsql", Version: "16", Managed: &managed},
	}
}

func TestCommandsFor(t *testing.T) {
	symfony := CommandsFor(config.FrameworkSymfony)
	if symfony.PhpConsole != "php bin/console" {
		t.Errorf("Symfony PhpConsole = %q", symfony.PhpConsole)
	}

	// The zero value must stay Symfony.
	if CommandsFor("") != symfony {
		t.Error("the empty framework must return the Symfony commands")
	}

	laravel := CommandsFor(config.FrameworkLaravel)
	if laravel.PhpConsole != "php artisan" {
		t.Errorf("Laravel PhpConsole = %q", laravel.PhpConsole)
	}
	if laravel.Optimize != "php artisan optimize" {
		t.Errorf("Laravel Optimize = %q", laravel.Optimize)
	}
}

func TestLaravelDBConnection(t *testing.T) {
	tests := map[string]string{
		"pgsql":   "pgsql",
		"mysql":   "mysql",
		"mariadb": "mariadb",
		"sqlite":  "sqlite",
	}
	for driver, want := range tests {
		if got := LaravelDBConnection(driver); got != want {
			t.Errorf("LaravelDBConnection(%q) = %q, want %q", driver, got, want)
		}
	}
}

func TestDockerfileGenerator_Laravel(t *testing.T) {
	gen := NewDockerfileGenerator(laravelConfig())
	dockerfile, err := gen.Generate()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	for _, want := range []string{
		"php artisan optimize:clear",
		"php artisan view:cache",
		"storage/framework/cache",
		"bootstrap/cache",
		"APP_ENV=production",
		"APP_DEBUG=false",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Laravel Dockerfile should contain %q", want)
		}
	}

	for _, forbidden := range []string{
		"bin/console",
		"symfony.lock",
		"cache:warmup",
		"importmap:install",
	} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Laravel Dockerfile must not contain %q", forbidden)
		}
	}
}

func TestDockerfileGenerator_GenerateEntrypoint_Laravel(t *testing.T) {
	gen := NewDockerfileGenerator(laravelConfig())
	entrypoint, err := gen.GenerateEntrypoint()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	for _, want := range []string{"php artisan optimize", "/app/storage", "/app/bootstrap/cache"} {
		if !strings.Contains(entrypoint, want) {
			t.Errorf("Laravel entrypoint should contain %q", want)
		}
	}
	if strings.Contains(entrypoint, "restore_assets") {
		t.Error("Laravel entrypoint must not restore Symfony assets")
	}
	if strings.Contains(entrypoint, "/app/var") {
		t.Error("Laravel entrypoint must not touch the Symfony var/ directory")
	}
}

func TestDockerfileGenerator_GenerateEntrypoint_SymfonyHasNoArtisan(t *testing.T) {
	cfg := &config.ProjectConfig{Name: "test-app", PHP: config.PHPConfig{Version: "8.3"}}
	gen := NewDockerfileGenerator(cfg)
	entrypoint, err := gen.GenerateEntrypoint()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if strings.Contains(entrypoint, "artisan") {
		t.Errorf("a Symfony entrypoint must not mention artisan:\n%s", entrypoint)
	}
	if strings.Contains(entrypoint, "/app/storage") {
		t.Error("a Symfony entrypoint must not reference Laravel storage")
	}
	if !strings.Contains(entrypoint, "restore_assets") {
		t.Error("a Symfony entrypoint must keep restore_assets")
	}
}

func TestDockerfileGenerator_GenerateDockerignore_Laravel(t *testing.T) {
	gen := NewDockerfileGenerator(laravelConfig())
	dockerignore, err := gen.GenerateDockerignore()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	for _, want := range []string{".env", "storage/logs", "bootstrap/cache"} {
		if !strings.Contains(dockerignore, want) {
			t.Errorf("Laravel .dockerignore should contain %q", want)
		}
	}
	// The Symfony-specific ignores must not leak into a Laravel image.
	if strings.Contains(dockerignore, "var/cache") {
		t.Error("Laravel .dockerignore must not exclude Symfony's var/cache")
	}
}

func TestDockerfileGenerator_GenerateDockerignore_SymfonyUnchanged(t *testing.T) {
	cfg := &config.ProjectConfig{Name: "test-app", PHP: config.PHPConfig{Version: "8.3"}}
	gen := NewDockerfileGenerator(cfg)
	dockerignore, err := gen.GenerateDockerignore()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}
	if !strings.Contains(dockerignore, "var/cache") {
		t.Error("Symfony .dockerignore must keep excluding var/cache")
	}
}

func TestComposeGenerator_GenerateDev_Laravel(t *testing.T) {
	cfg := laravelConfig()
	gen := NewComposeGenerator(cfg)
	dev, err := gen.GenerateDev()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(dev, "APP_ENV: local") {
		t.Errorf("Laravel dev compose should set APP_ENV=local:\n%s", dev)
	}
	if !strings.Contains(dev, "DB_CONNECTION: \"pgsql\"") {
		t.Errorf("Laravel dev compose should set DB_CONNECTION:\n%s", dev)
	}
	if strings.Contains(dev, "rabbitmq") {
		t.Error("a Laravel project must not get the Symfony Messenger rabbitmq service")
	}
}

func TestComposeGenerator_GenerateProd_Laravel(t *testing.T) {
	cfg := laravelConfig()
	cfg.Deploy.Domain = "example.com"
	gen := NewComposeGenerator(cfg)
	prod, err := gen.GenerateProd()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}

	if !strings.Contains(prod, "APP_ENV: production") {
		t.Errorf("Laravel prod compose should set APP_ENV=production:\n%s", prod)
	}
	if !strings.Contains(prod, "DB_CONNECTION: \"pgsql\"") {
		t.Errorf("Laravel prod compose should set DB_CONNECTION for a managed database:\n%s", prod)
	}
	// Laravel does not read DATABASE_URL: the prod compose must carry the
	// individual variables of the database service.
	for _, want := range []string{
		"DB_HOST: \"database\"",
		"DB_PORT: \"5432\"",
		"DB_DATABASE: \"${DATABASE_NAME:-test-app}\"",
		"DB_USERNAME: \"${DATABASE_USER:-test-app}\"",
	} {
		if !strings.Contains(prod, want) {
			t.Errorf("Laravel prod compose should contain %q:\n%s", want, prod)
		}
	}
}

func TestComposeGenerator_GenerateProd_SymfonyHasNoLaravelDBVars(t *testing.T) {
	managed := true
	cfg := &config.ProjectConfig{
		Name:     "test-app",
		Database: config.DatabaseConfig{Driver: "pgsql", Version: "16", Managed: &managed},
	}
	gen := NewComposeGenerator(cfg)
	prod, err := gen.GenerateProd()
	if err != nil {
		t.Fatalf("failed to generate: %v", err)
	}
	if strings.Contains(prod, "DB_CONNECTION") || strings.Contains(prod, "DB_HOST") {
		t.Errorf("a Symfony prod compose must not emit Laravel DB variables:\n%s", prod)
	}
}

func TestComposeGenerator_BuildDatabaseURL_Laravel(t *testing.T) {
	cfg := laravelConfig()
	gen := NewComposeGenerator(cfg)
	url, err := gen.buildDatabaseURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(url, "pgsql://") {
		t.Errorf("Laravel DATABASE_URL should use the Laravel driver scheme, got %q", url)
	}
	if strings.Contains(url, "serverVersion") || strings.Contains(url, "charset") {
		t.Errorf("Laravel DATABASE_URL must not carry Doctrine query parameters, got %q", url)
	}
}

func TestComposeGenerator_BuildDatabaseURL_SymfonyUnchanged(t *testing.T) {
	cfg := &config.ProjectConfig{
		Name:     "test-app",
		Database: config.DatabaseConfig{Driver: "pgsql", Version: "16"},
	}
	gen := NewComposeGenerator(cfg)
	url, err := gen.buildDatabaseURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(url, "postgresql://") || !strings.Contains(url, "serverVersion=16") {
		t.Errorf("Symfony DATABASE_URL must stay a Doctrine URL, got %q", url)
	}
}
