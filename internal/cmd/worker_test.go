package cmd

import (
	"strings"
	"testing"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
)

func TestWorkerCommand_Symfony(t *testing.T) {
	cfg := &config.ProjectConfig{
		Framework: config.FrameworkSymfony,
		Messenger: config.MessengerConfig{Enabled: true, Transports: []string{"async", "failed"}},
	}

	cmd, enabled := workerCommand(cfg)
	if !enabled {
		t.Fatal("workerCommand() should be enabled for an enabled Messenger")
	}
	if !strings.Contains(cmd, "messenger:consume async failed") {
		t.Errorf("unexpected Symfony worker command: %s", cmd)
	}
	if strings.Contains(cmd, "artisan") {
		t.Errorf("Symfony worker must not use artisan: %s", cmd)
	}
}

func TestWorkerCommand_SymfonyDefaultTransport(t *testing.T) {
	cfg := &config.ProjectConfig{
		Messenger: config.MessengerConfig{Enabled: true},
	}

	cmd, enabled := workerCommand(cfg)
	if !enabled {
		t.Fatal("workerCommand() should be enabled")
	}
	if !strings.Contains(cmd, "messenger:consume async") {
		t.Errorf("empty transports should default to async: %s", cmd)
	}
}

func TestWorkerCommand_Laravel(t *testing.T) {
	cfg := &config.ProjectConfig{
		Framework: config.FrameworkLaravel,
		Queue:     config.QueueConfig{Enabled: true, Connection: "redis"},
	}

	cmd, enabled := workerCommand(cfg)
	if !enabled {
		t.Fatal("workerCommand() should be enabled for an enabled queue")
	}
	if !strings.Contains(cmd, "php artisan queue:work") {
		t.Errorf("unexpected Laravel worker command: %s", cmd)
	}
	if strings.Contains(cmd, "messenger:consume") {
		t.Errorf("Laravel worker must not use messenger:consume: %s", cmd)
	}
	if strings.Contains(cmd, "--queue=") {
		t.Errorf("no --queue flag expected without configured queues: %s", cmd)
	}
}

func TestWorkerCommand_LaravelQueues(t *testing.T) {
	cfg := &config.ProjectConfig{
		Framework: config.FrameworkLaravel,
		Queue:     config.QueueConfig{Enabled: true, Queues: []string{"high", "default"}},
	}

	cmd, _ := workerCommand(cfg)
	if !strings.Contains(cmd, "--queue=high,default") {
		t.Errorf("configured queues should be joined with commas: %s", cmd)
	}
}

func TestWorkerCommand_Disabled(t *testing.T) {
	if _, enabled := workerCommand(&config.ProjectConfig{}); enabled {
		t.Error("workerCommand() should be disabled without Messenger or Queue")
	}

	laravel := &config.ProjectConfig{Framework: config.FrameworkLaravel}
	if _, enabled := workerCommand(laravel); enabled {
		t.Error("workerCommand() should be disabled for a Laravel app without a queue")
	}
}

func TestBuildAppRunCommand_LaravelEnv(t *testing.T) {
	managed := true
	cfg := &config.ProjectConfig{
		Name:      "myapp",
		Framework: config.FrameworkLaravel,
		Database:  config.DatabaseConfig{Driver: "pgsql", Managed: &managed},
	}

	cmd := buildAppRunCommand(cfg, "myapp:t1", "/opt/frankendeploy/apps/myapp",
		"postgresql://myapp:hex1234@myapp-db:5432/myapp?serverVersion=16&charset=utf8", "myapp-new")

	for _, want := range []string{
		"-e APP_ENV=production",
		"-e APP_DEBUG=false",
		"-e DB_CONNECTION=pgsql",
		"-e DB_HOST='myapp-db'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("Laravel docker run missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "APP_ENV=prod ") {
		t.Errorf("Laravel must use APP_ENV=production, not prod:\n%s", cmd)
	}
}

func TestBuildAppRunCommand_SymfonyEnvUnchanged(t *testing.T) {
	cfg := &config.ProjectConfig{Name: "myapp"}

	cmd := buildAppRunCommand(cfg, "myapp:t1", "/opt/frankendeploy/apps/myapp", "", "myapp-new")

	if !strings.Contains(cmd, "-e APP_ENV=prod -e APP_DEBUG=0") {
		t.Errorf("Symfony env defaults must not change:\n%s", cmd)
	}
	if strings.Contains(cmd, "DB_CONNECTION") {
		t.Errorf("no DB_CONNECTION expected without a Laravel managed database:\n%s", cmd)
	}
}
