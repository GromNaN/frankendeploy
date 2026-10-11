package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
)

// Scanner analyzes a Symfony project
type Scanner struct {
	projectPath string
}

// New creates a new Scanner for the given project path
func New(projectPath string) *Scanner {
	if projectPath == "" {
		projectPath = "."
	}
	return &Scanner{projectPath: projectPath}
}

// Scan performs a full project scan and returns the result
func (s *Scanner) Scan() (*config.ScanResult, error) {
	result := &config.ScanResult{}

	// Resolve the framework: Symfony is proven by symfony/framework-bundle,
	// Laravel by laravel/framework (or artisan + bootstrap/app.php).
	switch {
	case s.IsSymfonyProject():
		result.Framework = config.FrameworkSymfony
		result.IsSymfony = true
	case s.IsLaravelProject():
		result.Framework = config.FrameworkLaravel
	default:
		return nil, fmt.Errorf("not a Symfony or Laravel project (composer.json not found, or neither symfony/framework-bundle nor laravel/framework present)")
	}

	// Parse composer.json
	composer, err := s.ParseComposer()
	if err != nil {
		return nil, fmt.Errorf("failed to parse composer.json: %w", err)
	}

	result.PHPVersion = composer.PHPVersion
	result.PHPExtensions = composer.Extensions
	if composer.PHPVersionWarning != "" {
		result.Warnings = append(result.Warnings, composer.PHPVersionWarning)
	}

	// Detect database (framework-aware)
	dbConfig, dbWarnings, err := s.DetectDatabase()
	if err == nil && dbConfig != nil {
		result.Database = *dbConfig
	}
	result.Warnings = append(result.Warnings, dbWarnings...)

	// Detect assets (shared: Vite and Webpack are used by both frameworks)
	assetsConfig, err := s.DetectAssets()
	if err == nil && assetsConfig != nil {
		result.Assets = *assetsConfig
		if assetsConfig.Tailwind {
			result.Warnings = append(result.Warnings,
				"symfonycasts/tailwind-bundle detected: tailwind:build will run before asset-map:compile (otherwise the site deploys without CSS)")
		}
	}

	// MongoDB detection is framework-neutral (Doctrine ODM, the mongodb
	// library or mongodb/laravel-mongodb).
	result.HasMongoDB = s.HasMongoDB()

	// Resolve the real Messenger transports instead of guessing "async":
	// a wrong transport name means a crash-looping worker in production
	var messengerInfo messengerTransportInfo
	if result.Framework == config.FrameworkLaravel {
		result.HasQueue, result.QueueConnection = s.DetectLaravelQueue()
		if result.HasQueue {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Laravel queue connection %q detected: a worker container will run 'php artisan queue:work'", result.QueueConnection))
		}
		result.HasLaravelScheduler = s.HasLaravelScheduler()
		if result.HasLaravelScheduler {
			result.Warnings = append(result.Warnings,
				"Laravel scheduled tasks detected: a scheduler container will run 'php artisan schedule:work'")
		}
	} else {
		// Detect Symfony components
		result.HasDoctrine = s.HasDoctrine()
		result.HasMigrations = composer.HasPackage("doctrine/doctrine-migrations-bundle")
		result.HasMessenger = s.HasMessenger()
		result.HasMailer = s.HasMailer()
		result.HasScheduler = composer.HasPackage("symfony/scheduler")

		if result.HasMessenger {
			env, _ := s.GetMergedEnv()
			messengerInfo = s.detectMessengerTransports(env)
			result.MessengerTransports = messengerInfo.Transports
			if len(messengerInfo.Transports) > 0 {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("Messenger: worker will consume transports [%s] (read from messenger.yaml)",
						strings.Join(messengerInfo.Transports, ", ")))
			} else {
				result.Warnings = append(result.Warnings,
					"messenger.yaml found but no async transport to consume: no worker container will run")
			}
		}
		if result.HasScheduler {
			result.Warnings = append(result.Warnings,
				"symfony/scheduler detected: a worker will consume scheduler_default so scheduled tasks actually run in production")
		}
		// api-platform/core (v2/v3) or api-platform/symfony (v4 split packages)
		result.HasAPIPlatform = composer.HasAnyPackage("api-platform/core", "api-platform/symfony")

		// FrankenPHP worker mode eligibility: symfony/runtime >= 7.4 ships the
		// worker runner natively, runtime/frankenphp-symfony is the historical way
		result.HasFrankenPHPRuntime = composer.HasFrankenPHPWorkerRuntime()
		if result.HasFrankenPHPRuntime {
			source := "symfony/runtime >= 7.4"
			if composer.HasLegacyFrankenPHPRuntime() {
				source = "runtime/frankenphp-symfony"
			}
			result.Warnings = append(result.Warnings,
				source+" detected: enabling FrankenPHP worker mode (frankenphp.worker: true in frankendeploy.yaml). "+
					"Worker mode keeps the Symfony kernel in memory between requests — the app must be stateless. "+
					"Set frankenphp.worker: false to opt out")
		}
	}

	// Add required extensions based on detected features
	result.PHPExtensions = s.enhanceExtensions(result.PHPExtensions, result, composer, messengerInfo)

	return result, nil
}

// IsSymfonyProject checks if the directory contains a Symfony project
func (s *Scanner) IsSymfonyProject() bool {
	composerPath := filepath.Join(s.projectPath, "composer.json")
	if _, err := os.Stat(composerPath); os.IsNotExist(err) {
		return false
	}

	composer, err := s.ParseComposer()
	if err != nil {
		return false
	}

	return composer.HasSymfony
}

// HasDoctrine checks if Doctrine ORM is installed
func (s *Scanner) HasDoctrine() bool {
	doctrinePath := filepath.Join(s.projectPath, "config", "packages", "doctrine.yaml")
	if _, err := os.Stat(doctrinePath); err == nil {
		return true
	}
	return false
}

// HasMessenger checks if Symfony Messenger is configured
func (s *Scanner) HasMessenger() bool {
	messengerPath := filepath.Join(s.projectPath, "config", "packages", "messenger.yaml")
	if _, err := os.Stat(messengerPath); err == nil {
		return true
	}
	return false
}

// HasMailer checks if Symfony Mailer is configured
func (s *Scanner) HasMailer() bool {
	mailerPath := filepath.Join(s.projectPath, "config", "packages", "mailer.yaml")
	if _, err := os.Stat(mailerPath); err == nil {
		return true
	}
	return false
}

// HasMongoDB checks if the project uses MongoDB (a MONGODB_URI in the
// environment, or Doctrine MongoDB ODM / the mongodb library in composer).
// This is independent from the database detection.
func (s *Scanner) HasMongoDB() bool {
	if env, err := s.GetMergedEnv(); err == nil {
		if _, ok := env["MONGODB_URI"]; ok {
			return true
		}
	}
	composer, err := s.ParseComposer()
	if err != nil {
		return false
	}
	return composer.HasAnyPackage(
		"doctrine/mongodb-odm", "doctrine/mongodb-odm-bundle",
		"mongodb/mongodb", "mongodb/laravel-mongodb",
	) || composer.HasPackage("ext-mongodb") || s.hasExtInPlatform("mongodb")
}

// packageExtensionHints maps composer packages (exact name or prefix ending
// with "/") to the PHP extensions they need at runtime. Every inference is
// announced through a scanner warning: correct or announced, never silent.
var packageExtensionHints = []struct {
	pkg  string // exact package name, or prefix when ending with "/"
	exts []string
}{
	{"liip/imagine-bundle", []string{"gd"}},
	{"snc/redis-bundle", []string{"redis"}},
	{"phpoffice/", []string{"gd", "zip"}},
}

// enhanceExtensions adds required PHP extensions based on detected features
func (s *Scanner) enhanceExtensions(extensions []string, result *config.ScanResult, composer *ComposerResult, messengerInfo messengerTransportInfo) []string {
	extMap := make(map[string]bool)
	for _, ext := range extensions {
		extMap[ext] = true
	}

	// Always include essential extensions
	essentials := []string{"intl", "opcache", "zip"}
	for _, ext := range essentials {
		if !extMap[ext] {
			extensions = append(extensions, ext)
			extMap[ext] = true
		}
	}

	// Infer extensions from installed packages (announced)
	if composer != nil {
		for _, hint := range packageExtensionHints {
			matched := ""
			if strings.HasSuffix(hint.pkg, "/") {
				// Smallest matching name, so the warning is stable
				for pkg := range composer.Packages {
					if strings.HasPrefix(pkg, hint.pkg) && (matched == "" || pkg < matched) {
						matched = pkg
					}
				}
			} else if composer.HasPackage(hint.pkg) {
				matched = hint.pkg
			}
			if matched == "" {
				continue
			}
			for _, ext := range hint.exts {
				if !extMap[ext] {
					extensions = append(extensions, ext)
					extMap[ext] = true
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("adding PHP extension %s (required by %s)", ext, matched))
				}
			}
		}
	}

	// Add database-specific extension. DetectDatabase already canonicalizes
	// the driver via config.NormalizeDBDriver, so only the canonical forms
	// (pgsql/mysql/sqlite) need handling here.
	switch result.Database.Driver {
	case "pgsql":
		if !extMap["pdo_pgsql"] {
			extensions = append(extensions, "pdo_pgsql")
		}
	case "mysql", "mariadb":
		if !extMap["pdo_mysql"] {
			extensions = append(extensions, "pdo_mysql")
		}
	case "sqlite":
		if !extMap["pdo_sqlite"] {
			extensions = append(extensions, "pdo_sqlite")
		}
	}

	// MongoDB (Doctrine MongoDB ODM / the mongodb library): the app always
	// needs the ext-mongodb extension to connect, whether to the local dev
	// container or an external MONGODB_URI.
	if result.HasMongoDB && !extMap["mongodb"] {
		extensions = append(extensions, "mongodb")
		extMap["mongodb"] = true
		result.Warnings = append(result.Warnings, "adding PHP extension mongodb (Doctrine MongoDB ODM / mongodb detected)")
	}

	// AMQP only when an amqp transport is actually configured — installing
	// the extension for every messenger.yaml (present in each webapp
	// skeleton) bloated images for nothing
	if messengerInfo.UsesAMQP && !extMap["amqp"] {
		extensions = append(extensions, "amqp")
		extMap["amqp"] = true
		result.Warnings = append(result.Warnings, "adding PHP extension amqp (amqp:// Messenger transport detected)")
	}
	if messengerInfo.UsesRedis && !extMap["redis"] {
		extensions = append(extensions, "redis")
		extMap["redis"] = true
		result.Warnings = append(result.Warnings, "adding PHP extension redis (redis:// Messenger transport detected)")
	}

	// Laravel queue on redis needs the redis extension too
	if result.HasQueue && result.QueueConnection == "redis" && !extMap["redis"] {
		extensions = append(extensions, "redis")
		extMap["redis"] = true
		result.Warnings = append(result.Warnings, "adding PHP extension redis (Laravel queue connection redis detected)")
	}

	// pcntl lets the worker (messenger:consume or queue:work) shut down
	// gracefully on SIGTERM during deployments instead of dying mid-message
	if (result.HasMessenger || result.HasQueue) && !extMap["pcntl"] {
		extensions = append(extensions, "pcntl")
		extMap["pcntl"] = true
		result.Warnings = append(result.Warnings, "adding PHP extension pcntl (graceful worker shutdown)")
	}

	// Deduplicate while preserving order
	seen := make(map[string]bool)
	deduped := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		if !seen[ext] {
			seen[ext] = true
			deduped = append(deduped, ext)
		}
	}

	return deduped
}

// ToProjectConfig converts scan result to project config
func (s *Scanner) ToProjectConfig(result *config.ScanResult, name string) *config.ProjectConfig {
	cfg := config.DefaultProjectConfig()
	cfg.Name = name
	cfg.Framework = result.Framework
	cfg.PHP.Version = result.PHPVersion
	cfg.PHP.Extensions = result.PHPExtensions
	cfg.Database = result.Database
	cfg.Assets = result.Assets

	if result.Framework == config.FrameworkLaravel {
		// Laravel storage conventions. bootstrap/cache and
		// storage/framework/views are deliberately NOT shared: they hold
		// config/route/view caches that must come from the new image, or a
		// new release would boot on the previous release's cached config.
		cfg.Deploy.SharedDirs = []string{
			"storage/app",
			"storage/framework/cache",
			"storage/framework/sessions",
			"storage/logs",
		}
		cfg.Deploy.SharedFiles = []string{".env"}

		if result.HasQueue {
			cfg.Queue = config.QueueConfig{Enabled: true, Connection: result.QueueConnection}
		}
		cfg.Scheduler = config.SchedulerConfig{Enabled: result.HasLaravelScheduler}
	} else {
		// A pure API Platform app typically returns 404 on "/" in prod, which
		// would fail the deploy health check: default to the API entrypoint.
		if result.HasAPIPlatform {
			cfg.Deploy.HealthcheckPath = "/api"
		}

		// Enable FrankenPHP worker mode when the app ships a worker runtime
		// (announced via a scanner warning — never silent)
		if result.HasFrankenPHPRuntime {
			cfg.FrankenPHP.Worker = true
		}

		// Auto-fill Messenger config from the transports actually configured
		// in messenger.yaml (never guess "async": a wrong transport name is a
		// crash-looping worker)
		if result.HasMessenger && len(result.MessengerTransports) > 0 {
			cfg.Messenger = config.MessengerConfig{
				Enabled:    true,
				Transports: result.MessengerTransports,
			}
		}

		// symfony/scheduler: its scheduler_default transport must be consumed
		// or scheduled tasks silently never run
		if result.HasScheduler {
			cfg.Messenger.Enabled = true
			if !contains(cfg.Messenger.Transports, "scheduler_default") {
				cfg.Messenger.Transports = append(cfg.Messenger.Transports, "scheduler_default")
			}
		}

		// Auto-fill Mailer config if detected
		if result.HasMailer {
			cfg.Mailer = config.MailerConfig{Enabled: true}
		}
	}

	// For SQLite, persist the database across releases. Symfony keeps the
	// directory; Laravel keeps the file, because sharing the whole database/
	// directory would hide database/migrations.
	if result.Database.Driver == "sqlite" && result.Database.Path != "" {
		if result.Framework == config.FrameworkLaravel {
			if !contains(cfg.Deploy.SharedFiles, result.Database.Path) {
				cfg.Deploy.SharedFiles = append(cfg.Deploy.SharedFiles, result.Database.Path)
			}
		} else {
			sqliteDir := getSQLiteDirectory(result.Database.Path)
			if sqliteDir != "" && !contains(cfg.Deploy.SharedDirs, sqliteDir) {
				cfg.Deploy.SharedDirs = append(cfg.Deploy.SharedDirs, sqliteDir)
			}
		}
	}

	// Auto-fill MongoDB service config if detected. Managed stays false by
	// default: the app connects to an external MONGODB_URI (e.g. Atlas) unless
	// the user opts into a locally provisioned container with managed: true.
	if result.HasMongoDB {
		cfg.MongoDB = config.MongoConfig{Enabled: true}
	}

	// Auto-fill hooks based on detected features
	cfg.Deploy.Hooks = s.generateDefaultHooks(result)

	return cfg
}

// contains checks if a string slice contains a specific value
func contains(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}

// generateDefaultHooks creates default deployment hooks based on detected features
func (s *Scanner) generateDefaultHooks(result *config.ScanResult) config.Hooks {
	hooks := config.Hooks{}

	if result.Framework == config.FrameworkLaravel {
		// Laravel always ships database/migrations. --force is mandatory in
		// production: without it Artisan prompts for confirmation and the
		// hook hangs until the deploy times out.
		if result.Database.Driver != "" {
			hooks.PreDeploy = append(hooks.PreDeploy, "php artisan migrate --force")
		}
		return hooks
	}

	// Doctrine alone is not enough: an app can use the ORM without the
	// migrations bundle (the Symfony Demo does), and the hook would fail
	if result.HasDoctrine && result.HasMigrations {
		hooks.PreDeploy = append(hooks.PreDeploy,
			"php bin/console doctrine:migrations:migrate --no-interaction --allow-no-migration")
	}

	// No default cache:warmup post_deploy hook: the cache is already warmed
	// at image build time (Dockerfile runs cache:warmup), and a post-deploy
	// warmup would run on the LIVE container after the swap.

	return hooks
}
