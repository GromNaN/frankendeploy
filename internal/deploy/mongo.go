package deploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yoanbernabeu/frankendeploy/internal/config"
	"github.com/yoanbernabeu/frankendeploy/internal/constants"
	"github.com/yoanbernabeu/frankendeploy/internal/security"
	"github.com/yoanbernabeu/frankendeploy/internal/ssh"
)

// mongoImage is the MongoDB community server image used for the managed
// mongodb service. The full name "mongodb" is used everywhere, never "mongo".
const mongoImage = "mongodb/mongodb-community-server:8"

// mongoContainerName is the fixed name of the managed mongodb container.
const mongoContainerName = "mongodb"

// mongoVolumeName is the persistent volume of the managed mongodb container.
const mongoVolumeName = "mongodb-data"

// mongoCredentialsFile returns the path where the managed mongodb MONGODB_URI
// is persisted for reuse across deploys, rollbacks and reloads.
func mongoCredentialsFile(appPath string) string {
	return filepath.Join(appPath, "shared", ".mongo_credentials")
}

// DeployMongoService ensures the managed mongodb container exists (when
// mongodb.managed is true) and returns the MONGODB_URI the app must use.
// Credentials are generated once, saved to shared/.mongo_credentials, and
// the existing container+credentials are reused on subsequent runs.
func DeployMongoService(ctx context.Context, client ssh.Executor, cfg *config.ProjectConfig, appPath string, log Logger) (string, error) {
	if log == nil {
		log = NopLogger{}
	}
	dbName := strings.ReplaceAll(cfg.Name, "-", "_")
	credentialsFile := mongoCredentialsFile(appPath)

	savedURL := ""
	if result, err := client.Exec(ctx, fmt.Sprintf("cat %s 2>/dev/null", credentialsFile)); err == nil && result != nil && result.ExitCode == 0 {
		savedURL = strings.TrimSpace(result.Stdout)
	}

	containerExists := false
	containerRunning := false
	if result, err := client.Exec(ctx, fmt.Sprintf("docker ps -aq -f name=^%s$", mongoContainerName)); err == nil && result != nil {
		containerExists = strings.TrimSpace(result.Stdout) != ""
	}
	if containerExists {
		if result, err := client.Exec(ctx, fmt.Sprintf("docker ps -q -f name=^%s$", mongoContainerName)); err == nil && result != nil {
			containerRunning = strings.TrimSpace(result.Stdout) != ""
		}
	}

	if containerExists && savedURL != "" {
		if !containerRunning {
			log.Info("mongodb container is stopped, starting it...")
			if _, err := client.Exec(ctx, fmt.Sprintf("docker start %s", mongoContainerName)); err != nil {
				return "", fmt.Errorf("failed to start mongodb container: %w", err)
			}
		}
		return savedURL, nil
	}

	// Fresh setup: generate credentials
	user := cfg.Name
	password, err := generateRandomPassword(24)
	if err != nil {
		return "", err
	}

	envArgs := fmt.Sprintf("-e MONGO_INITDB_ROOT_USERNAME=%s -e MONGO_INITDB_ROOT_PASSWORD=%s -e MONGO_INITDB_DATABASE=%s",
		security.ShellEscape(user), security.ShellEscape(password), security.ShellEscape(dbName))
	// Single-node replica set (rs0): the driver connects with replicaSet=rs0
	// and transactions are available. Set for a single node only, so there is
	// no data redundancy.
	databaseURL := fmt.Sprintf("mongodb://%s:%s@%s:27017/%s?authSource=admin&replicaSet=rs0",
		user, password, mongoContainerName, dbName)

	// Remove any leftover container before recreation
	_, _ = client.Exec(ctx, fmt.Sprintf("docker stop %s 2>/dev/null || true", mongoContainerName))
	_, _ = client.Exec(ctx, fmt.Sprintf("docker rm %s 2>/dev/null || true", mongoContainerName))

	runCmd := fmt.Sprintf(`docker run -d --name %s \
		--network %s \
		--restart unless-stopped \
		%s \
		%s \
		-v %s:%s \
		%s --replSet rs0`,
		mongoContainerName,
		constants.AppNetworkName(cfg.Name),
		constants.DockerLogOptions,
		envArgs,
		mongoVolumeName,
		"/data/db",
		mongoImage)
	if result, err := client.Exec(ctx, runCmd); err != nil {
		return "", fmt.Errorf("failed to start mongodb container: %w", err)
	} else if err := result.Err(); err != nil {
		return "", fmt.Errorf("failed to start mongodb container: %w", err)
	}

	// Save credentials for reuse
	if _, err := client.Exec(ctx, fmt.Sprintf("echo %s > %s", security.ShellEscape(databaseURL), credentialsFile)); err != nil {
		return "", fmt.Errorf("failed to save mongodb credentials: %w", err)
	}
	if _, err := client.Exec(ctx, fmt.Sprintf("chmod 600 %s", credentialsFile)); err != nil {
		log.Warning("Could not set permissions on credentials file: %v", err)
	}

	log.Info("Waiting for mongodb to be ready...")
	for i := 0; i < DBReadinessAttempts; i++ {
		pingCmd := fmt.Sprintf("docker exec %s mongosh -u %s -p %s --authenticationDatabase admin --quiet --eval \"db.adminCommand({ping:1}).ok\"",
			mongoContainerName, security.ShellEscape(user), security.ShellEscape(password))
		checkResult, _ := client.Exec(ctx, pingCmd)
		if checkResult != nil && checkResult.ExitCode == 0 {
			if err := initiateMongoReplicaSet(ctx, client, user, password); err != nil {
				return "", err
			}
			return databaseURL, nil
		}
		time.Sleep(1 * time.Second)
	}
	return "", fmt.Errorf("mongodb %s did not become ready after %d seconds — check its logs: docker logs %s",
		mongoContainerName, DBReadinessAttempts, mongoContainerName)
}

// initiateMongoReplicaSet turns the fresh mongodb node into a single-node
// replica set (rs0) and waits for it to become PRIMARY (transactions require
// a primary).
func initiateMongoReplicaSet(ctx context.Context, client ssh.Executor, user, password string) error {
	initCmd := fmt.Sprintf("docker exec %s mongosh -u %s -p %s --authenticationDatabase admin --quiet --eval \"rs.initiate()\"",
		mongoContainerName, security.ShellEscape(user), security.ShellEscape(password))
	if _, err := client.Exec(ctx, initCmd); err != nil {
		return fmt.Errorf("failed to initiate mongodb replica set: %w", err)
	}
	for i := 0; i < DBReadinessAttempts; i++ {
		statusCmd := fmt.Sprintf("docker exec %s mongosh -u %s -p %s --authenticationDatabase admin --quiet --eval \"rs.status().members[0].stateStr\"",
			mongoContainerName, security.ShellEscape(user), security.ShellEscape(password))
		checkResult, _ := client.Exec(ctx, statusCmd)
		if checkResult != nil && checkResult.ExitCode == 0 && strings.Contains(checkResult.Stdout, "PRIMARY") {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("mongodb replica set did not become PRIMARY after %d seconds — check its logs: docker logs %s",
		DBReadinessAttempts, mongoContainerName)
}

// readSavedMongoURL returns the MONGODB_URI persisted by a managed mongodb
// deploy (shared/.mongo_credentials). Empty when absent.
func readSavedMongoURL(ctx context.Context, client ssh.Executor, appPath string) string {
	result, err := client.Exec(ctx, fmt.Sprintf("cat %s 2>/dev/null", mongoCredentialsFile(appPath)))
	if err != nil || result == nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// ManagedMongoEnvVar returns a "MONGODB_URI=<url>" docker env pair to inject
// into the app when it uses a managed mongodb service, or "" otherwise (the
// caller provides an external MONGODB_URI).
func ManagedMongoEnvVar(ctx context.Context, client ssh.Executor, cfg *config.ProjectConfig, appPath string) string {
	if !cfg.MongoDB.Enabled || !cfg.MongoDB.Managed {
		return ""
	}
	url := readSavedMongoURL(ctx, client, appPath)
	if url == "" {
		return ""
	}
	return "MONGODB_URI=" + security.ShellEscape(url)
}
