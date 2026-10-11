package deploy

import (
	"strings"
	"testing"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
)

func TestNeedsDatabaseURL(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.ProjectConfig
		expected bool
	}{
		{
			name: "no database configured",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{},
			},
			expected: false,
		},
		{
			name: "managed database",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{
					Driver:  "pgsql",
					Managed: boolPtr(true),
				},
			},
			expected: false,
		},
		{
			name: "sqlite database",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{
					Driver: "sqlite",
				},
			},
			expected: false,
		},
		{
			name: "pdo_sqlite database",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{
					Driver: "pdo_sqlite",
				},
			},
			expected: false,
		},
		{
			name: "external pgsql database",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{
					Driver:  "pgsql",
					Managed: boolPtr(false),
				},
			},
			expected: true,
		},
		{
			name: "external mysql database",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{
					Driver:  "mysql",
					Managed: boolPtr(false),
				},
			},
			expected: true,
		},
		{
			name: "database with no managed flag (defaults to external)",
			cfg: &config.ProjectConfig{
				Database: config.DatabaseConfig{
					Driver: "pgsql",
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := needsDatabaseURL(tt.cfg)
			if result != tt.expected {
				t.Errorf("needsDatabaseURL() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestGenerateSymfonySecret(t *testing.T) {
	secret, err := GenerateSymfonySecret()
	if err != nil {
		t.Fatalf("GenerateSymfonySecret() error = %v", err)
	}

	// Check length (64 hex characters = 32 bytes)
	if len(secret) != 64 {
		t.Errorf("GenerateSymfonySecret() length = %d, expected 64", len(secret))
	}

	// Check it's valid hex
	for _, c := range secret {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("GenerateSymfonySecret() contains invalid hex character: %c", c)
		}
	}

	// Check uniqueness (generate another and compare)
	secret2, err := GenerateSymfonySecret()
	if err != nil {
		t.Fatalf("GenerateSymfonySecret() second call error = %v", err)
	}
	if secret == secret2 {
		t.Error("GenerateSymfonySecret() generated same secret twice")
	}
}

func TestGenerateMissingSecrets(t *testing.T) {
	tests := []struct {
		name      string
		missing   []EnvRequirement
		wantKeys  []string
		wantEmpty bool
	}{
		{
			name:      "empty missing list",
			missing:   []EnvRequirement{},
			wantKeys:  []string{},
			wantEmpty: true,
		},
		{
			name: "APP_SECRET can be generated",
			missing: []EnvRequirement{
				{Name: "APP_SECRET", CanGenerate: true},
			},
			wantKeys:  []string{"APP_SECRET"},
			wantEmpty: false,
		},
		{
			name: "DATABASE_URL cannot be generated",
			missing: []EnvRequirement{
				{Name: "DATABASE_URL", CanGenerate: false},
			},
			wantKeys:  []string{},
			wantEmpty: true,
		},
		{
			name: "mixed requirements",
			missing: []EnvRequirement{
				{Name: "APP_SECRET", CanGenerate: true},
				{Name: "DATABASE_URL", CanGenerate: false},
			},
			wantKeys:  []string{"APP_SECRET"},
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generated, err := GenerateMissingSecrets(tt.missing)
			if err != nil {
				t.Fatalf("GenerateMissingSecrets() error = %v", err)
			}

			if tt.wantEmpty && len(generated) > 0 {
				t.Errorf("GenerateMissingSecrets() = %v, expected empty map", generated)
			}

			for _, key := range tt.wantKeys {
				if _, exists := generated[key]; !exists {
					t.Errorf("GenerateMissingSecrets() missing key %s", key)
				}
			}
		})
	}
}

func TestFormatEnvCheckError(t *testing.T) {
	missing := []EnvRequirement{
		{Name: "APP_SECRET", Description: "Symfony security secret"},
		{Name: "DATABASE_URL", Description: "Database connection URL"},
	}

	result := FormatEnvCheckError(missing, "prod")

	// Check that all required elements are present
	expectedStrings := []string{
		"Missing required environment variables",
		"APP_SECRET",
		"DATABASE_URL",
		"frankendeploy env set prod APP_SECRET",
		"frankendeploy env set prod DATABASE_URL",
		"--force",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(result, expected) {
			t.Errorf("FormatEnvCheckError() missing expected string: %s", expected)
		}
	}
}

func TestFormatEnvCheckError_MongoDBURL(t *testing.T) {
	missing := []EnvRequirement{
		{Name: "MONGODB_URI", Description: "MongoDB connection URL"},
	}

	result := FormatEnvCheckError(missing, "prod")

	if !strings.Contains(result, "frankendeploy env set prod MONGODB_URI=") {
		t.Errorf("FormatEnvCheckError() should suggest setting MONGODB_URI, got:\n%s", result)
	}
}

func TestParseEnvContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected map[string]string
	}{
		{
			name:     "empty content",
			content:  "",
			expected: map[string]string{},
		},
		{
			name:    "simple key=value",
			content: "APP_ENV=prod",
			expected: map[string]string{
				"APP_ENV": "prod",
			},
		},
		{
			name:    "quoted value",
			content: `DATABASE_URL="postgresql://user:pass@host/db"`,
			expected: map[string]string{
				"DATABASE_URL": "postgresql://user:pass@host/db",
			},
		},
		{
			name:    "multiple variables",
			content: "APP_ENV=prod\nAPP_SECRET=mysecret",
			expected: map[string]string{
				"APP_ENV":    "prod",
				"APP_SECRET": "mysecret",
			},
		},
		{
			name:    "with comments",
			content: "# This is a comment\nAPP_ENV=prod",
			expected: map[string]string{
				"APP_ENV": "prod",
			},
		},
		{
			name:    "with empty lines",
			content: "APP_ENV=prod\n\nAPP_SECRET=secret",
			expected: map[string]string{
				"APP_ENV":    "prod",
				"APP_SECRET": "secret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseEnvContent(tt.content)
			if len(result) != len(tt.expected) {
				t.Errorf("ParseEnvContent() returned %d items, expected %d", len(result), len(tt.expected))
			}
			for key, expectedValue := range tt.expected {
				if result[key] != expectedValue {
					t.Errorf("ParseEnvContent()[%s] = %s, expected %s", key, result[key], expectedValue)
				}
			}
		})
	}
}

func TestBuildEnvContent(t *testing.T) {
	vars := map[string]string{
		"APP_ENV": "prod",
	}

	result := BuildEnvContent(vars)

	if !strings.Contains(result, "APP_ENV=prod") {
		t.Errorf("BuildEnvContent() = %s, expected to contain APP_ENV=prod", result)
	}

	// Check it ends with newline
	if !strings.HasSuffix(result, "\n") {
		t.Error("BuildEnvContent() should end with newline")
	}
}

// TestBuildEnvContent_DeterministicOrdering guards the fix for non-deterministic
// ordering that produced noisy diffs on every env file rewrite.
func TestBuildEnvContent_DeterministicOrdering(t *testing.T) {
	vars := map[string]string{
		"ZEBRA":        "z",
		"APP_ENV":      "prod",
		"DATABASE_URL": "postgres://u:p@h/db",
		"APP_SECRET":   "abc",
	}

	// Calling buildEnvContent repeatedly must produce the exact same bytes.
	first := BuildEnvContent(vars)
	for i := 0; i < 20; i++ {
		if got := BuildEnvContent(vars); got != first {
			t.Fatalf("BuildEnvContent() is non-deterministic:\nfirst=%q\ngot  =%q", first, got)
		}
	}

	// Keys must appear in alphabetical order.
	want := "APP_ENV=prod\nAPP_SECRET=abc\nDATABASE_URL=postgres://u:p@h/db\nZEBRA=z\n"
	if first != want {
		t.Errorf("BuildEnvContent() = %q, want %q", first, want)
	}
}

func TestGenerateLaravelAppKey(t *testing.T) {
	key, err := GenerateLaravelAppKey()
	if err != nil {
		t.Fatalf("GenerateLaravelAppKey() error = %v", err)
	}

	if !strings.HasPrefix(key, "base64:") {
		t.Errorf("GenerateLaravelAppKey() = %q, want a base64: prefix", key)
	}
	// "base64:" + base64(32 bytes) = 7 + 44 characters
	if len(key) != 51 {
		t.Errorf("GenerateLaravelAppKey() length = %d, want 51", len(key))
	}

	key2, err := GenerateLaravelAppKey()
	if err != nil {
		t.Fatalf("GenerateLaravelAppKey() second call error = %v", err)
	}
	if key == key2 {
		t.Error("GenerateLaravelAppKey() generated the same key twice")
	}
}

func TestFrameworkEnvRequirements(t *testing.T) {
	symfony := FrameworkEnvRequirements[config.FrameworkSymfony]
	laravel := FrameworkEnvRequirements[config.FrameworkLaravel]

	if !hasRequirement(symfony, "APP_SECRET") {
		t.Error("Symfony requirements must include APP_SECRET")
	}
	if hasRequirement(symfony, "APP_KEY") {
		t.Error("Symfony requirements must not include Laravel's APP_KEY")
	}
	if !hasRequirement(laravel, "APP_KEY") {
		t.Error("Laravel requirements must include APP_KEY")
	}
	if hasRequirement(laravel, "APP_SECRET") {
		t.Error("Laravel requirements must not include Symfony's APP_SECRET")
	}
	// Laravel's convention is APP_ENV=production, not "prod".
	if v := requirementDefault(laravel, "APP_ENV"); v != "production" {
		t.Errorf("Laravel APP_ENV default = %q, want production", v)
	}
	if v := requirementDefault(laravel, "APP_DEBUG"); v != "false" {
		t.Errorf("Laravel APP_DEBUG default = %q, want false", v)
	}
}

func TestLaravelDatabaseEnvVars(t *testing.T) {
	managed := true
	laravelManaged := &config.ProjectConfig{
		Framework: config.FrameworkLaravel,
		Database:  config.DatabaseConfig{Driver: "pgsql", Managed: &managed},
	}
	laravelExternal := &config.ProjectConfig{
		Framework: config.FrameworkLaravel,
		Database:  config.DatabaseConfig{Driver: "pgsql", Managed: boolPtr(false)},
	}
	symfonyManaged := &config.ProjectConfig{
		Framework: config.FrameworkSymfony,
		Database:  config.DatabaseConfig{Driver: "pgsql", Managed: &managed},
	}

	tests := []struct {
		name string
		cfg  *config.ProjectConfig
		url  string
		want []string
	}{
		{
			name: "laravel managed pgsql",
			cfg:  laravelManaged,
			url:  "postgresql://myapp:hex1234@myapp-db:5432/myapp?serverVersion=16&charset=utf8",
			want: []string{
				"DB_CONNECTION=pgsql",
				"DB_HOST='myapp-db'",
				"DB_PORT='5432'",
				"DB_DATABASE='myapp'",
				"DB_USERNAME='myapp'",
				"DB_PASSWORD='hex1234'",
			},
		},
		{
			name: "laravel managed mysql",
			cfg: &config.ProjectConfig{
				Framework: config.FrameworkLaravel,
				Database:  config.DatabaseConfig{Driver: "mysql", Managed: &managed},
			},
			url: "mysql://u:p@db:3306/app?serverVersion=8.0",
			want: []string{
				"DB_CONNECTION=mysql",
				"DB_HOST='db'",
				"DB_PORT='3306'",
				"DB_DATABASE='app'",
				"DB_USERNAME='u'",
				"DB_PASSWORD='p'",
			},
		},
		{
			name: "laravel external database is never overridden",
			cfg:  laravelExternal,
			url:  "postgresql://u:p@external:5432/db",
			want: nil,
		},
		{
			name: "symfony managed database is not touched",
			cfg:  symfonyManaged,
			url:  "postgresql://u:p@db:5432/db",
			want: nil,
		},
		{
			name: "empty url",
			cfg:  laravelManaged,
			url:  "",
			want: nil,
		},
		{
			name: "unparseable url",
			cfg:  laravelManaged,
			url:  "not a url",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LaravelDatabaseEnvVars(tt.cfg, tt.url)
			if len(got) != len(tt.want) {
				t.Fatalf("LaravelDatabaseEnvVars() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("LaravelDatabaseEnvVars()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func hasRequirement(reqs []EnvRequirement, name string) bool {
	for _, r := range reqs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func requirementDefault(reqs []EnvRequirement, name string) string {
	for _, r := range reqs {
		if r.Name == name {
			return r.DefaultValue
		}
	}
	return ""
}

// Helper function for bool pointers
func boolPtr(b bool) *bool {
	return &b
}
