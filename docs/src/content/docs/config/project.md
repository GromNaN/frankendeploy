---
title: frankendeploy.yaml
description: Project configuration reference
---

## Overview

`frankendeploy.yaml`, at the root of your project, describes how FrankenDeploy builds and deploys the application. `frankendeploy init` writes it from what it detects; every field below can be edited by hand.

The file is validated each time it is loaded. Unknown fields are rejected with an explicit error, so a typo never fails silently.

## Complete Reference

Every field, with its default. Only `name` and `php.version` are required.

```yaml
# Application name (required)
# Used for the Docker image, the container names, the directories on the server
# Lowercase letters, digits and hyphens only
name: my-app

# Application framework: symfony or laravel (optional, default: symfony)
# Set by init from composer.json. An older configuration without this field
# is treated as Symfony.
framework: symfony

# FrankenPHP image tag to build from (optional, default: "1", the latest 1.x)
# Pin a version when you need reproducible builds
frankenphp_version: "1"

# FrankenPHP runtime options (optional)
frankenphp:
  # Worker mode: boots the Symfony kernel once and keeps it in memory
  # between requests. Requires symfony/runtime >= 7.4 or the
  # runtime/frankenphp-symfony package. Auto-enabled by init when detected.
  worker: false

# PHP configuration
php:
  # PHP version: 8.2, 8.3 or 8.4 (FrankenPHP requires PHP >= 8.2)
  version: "8.3"

  # PHP extensions to install in the image (dev and prod)
  extensions:
    - intl
    - opcache
    - zip
    - pdo_pgsql

  # Custom php.ini values
  ini_values:
    - "memory_limit=256M"
    - "upload_max_filesize=50M"

# Database (optional)
database:
  # Driver: pgsql, mysql, mariadb or sqlite
  driver: pgsql

  # Version of the database Docker image
  version: "16"

  # Managed: FrankenDeploy creates and maintains the database container
  # in production and injects DATABASE_URL (default: true for pgsql,
  # mysql and mariadb; not available for sqlite)
  managed: true

  # SQLite only: path of the database file, relative to the project root
  # path: var/data.db

  # Local development only: override the host, port and name used in the
  # generated DATABASE_URL of compose.yaml (rarely needed)
  # host: database
  # port: 5432
  # name: app

# Symfony Messenger (optional)
messenger:
  # Deploy a dedicated worker container running messenger:consume
  enabled: false

  # Transports to consume (filled by init from config/packages/messenger.yaml)
  transports:
    - async

# Laravel queue worker (optional)
queue:
  # Deploy a dedicated worker container running queue:work
  enabled: false

  # Connection name (database, redis, ...); filled by init from QUEUE_CONNECTION
  connection: redis

  # Queues to consume, joined into --queue=high,default (default: all)
  queues:
    - default

# Laravel scheduler (optional)
scheduler:
  # Deploy a dedicated container running php artisan schedule:work
  enabled: false

# Symfony Mailer (optional): adds Mailpit to the local dev environment
mailer:
  enabled: false

# MongoDB (optional), separate from the database section
mongodb:
  # Adds a local mongodb container in dev, and the mongodb PHP extension
  enabled: false
  # managed: true provisions a single-node replica set in production and
  # injects MONGODB_URI. false expects an external MONGODB_URI (e.g. Atlas)
  managed: false

# Dockerfile customization (optional)
dockerfile:
  # Additional APT packages
  extra_packages:
    - imagemagick

  # Raw Dockerfile instructions, appended to the base stage
  extra_commands:
    - "RUN install-php-extensions imagick"

# Asset build (optional)
assets:
  # Build tool: npm, yarn, pnpm or assetmapper
  build_tool: npm

  # Command to build assets (npm, yarn, pnpm)
  build_command: "npm run build"

  # Output directory, relative to the project root
  output_dir: "public/build"

  # Node.js version of the build stage (default: 22)
  node_version: "22"

  # AssetMapper + symfonycasts/tailwind-bundle: run tailwind:build before
  # asset-map:compile (auto-detected by init)
  tailwind: false

# Deployment
deploy:
  # Public domain served by Caddy with automatic HTTPS. Optional: without
  # it the app runs on the server but is not publicly reachable.
  domain: my-app.com

  # Health check endpoint (default: /, or /api when API Platform is detected)
  healthcheck_path: /health

  # Health check window (0 or omitted = default)
  healthcheck_timeout: 90    # overall window in seconds
  healthcheck_retries: 30    # max attempts
  healthcheck_interval: 3    # seconds between attempts

  # Releases, Docker images and database backups to keep (default: 5)
  keep_releases: 5

  # Files and directories shared between releases
  shared_files:
    - .env.local
  shared_dirs:
    - var/log
    - var/sessions

  # Optional resource limits of the app container
  memory_limit: 512m
  cpu_limit: "1.5"

  # Deployment hooks, run inside the container
  hooks:
    # In the NEW container, before traffic switches to it
    pre_deploy:
      - php bin/console doctrine:migrations:migrate --no-interaction --allow-no-migration
    # In the live container, after the switch
    post_deploy: []

# Environment variables written into the generated Compose files
env:
  # compose.yaml (local development)
  dev:
    APP_DEBUG: "1"
    MAILER_DSN: "smtp://mailpit:1025"
  # compose.prod.yaml only. NOT used by `frankendeploy deploy`:
  # production variables are set on the server with `frankendeploy env set`
  prod:
    APP_DEBUG: "0"
```

## Field Details

### `name` (required)

Lowercase letters, digits and hyphens. Used as the Docker image name, the container names (`<name>`, `<name>-worker`, `<name>-db`), the directory `/opt/frankendeploy/apps/<name>` and the Docker network `frankendeploy-<name>` on the server. Must be unique per server.

### `framework`

`symfony` (default) or `laravel`. `init` sets it from `composer.json`: `symfony/framework-bundle` for Symfony, `laravel/framework` (or `artisan` plus `bootstrap/app.php`) for Laravel. A project that matches neither is refused with an explicit error. A configuration written before Laravel support has no `framework` field and is treated as Symfony, so existing projects keep behaving exactly as before.

The framework drives the console commands (`php bin/console` or `php artisan`), the storage directories, the default environment values (`prod`/`0` for Symfony, `production`/`false` for Laravel), the migration hook and the database environment.

### `frankenphp_version`

Tag of the `dunglas/frankenphp` image the Dockerfile builds from (`FROM dunglas/frankenphp:<frankenphp_version>-php<php.version>`). Default `1`, which follows the latest 1.x release. Pin it (for instance `1.4`) when you need every build to use the same FrankenPHP.

### `frankenphp.worker`

FrankenPHP worker mode boots the Symfony kernel **once** and keeps it in memory between requests, removing the per-request bootstrap cost. On a 2 vCPU VPS the same load test went from 23 to 53 requests per second, p95 from 5.5 s to 0.83 s.

```yaml
frankenphp:
  worker: true
```

- A worker runtime must be installed: `symfony/runtime` >= 7.4 ships it natively; older apps can use the `runtime/frankenphp-symfony` package. `build` and `deploy` fail with a clear message if neither is present.
- **The application must be stateless**: static properties survive between requests. Symfony services are reset automatically, hand-written static caches are not.
- Memory leaks surface as automatic worker restarts.
- `init` enables it automatically (with a warning) when a worker runtime is detected. Opt out with `worker: false`.
- Production only: the dev environment keeps classic mode for easier debugging.
- **Laravel is not supported in worker mode**: it needs Laravel Octane with the FrankenPHP driver. `build` fails with a clear message when a Laravel project sets `worker: true`.

When enabled, `build` generates a `Caddyfile` at the project root that is baked into the production image.

### `php.version`

`8.2`, `8.3` or `8.4`. FrankenPHP requires PHP >= 8.2: if `composer.json` allows older versions, `init` floors the detected version at 8.2 and warns you.

### `php.extensions`

Installed with `install-php-extensions` in the base image, so they are present in **both** dev and prod. `init` always adds `intl`, `opcache` and `zip`, the PDO driver of your database, `amqp` or `redis` when a Messenger transport or a Laravel queue connection uses them, and `pcntl` when a worker runs (graceful shutdown).

Common extensions: `pdo_pgsql`, `pdo_mysql`, `pdo_sqlite`, `mongodb`, `intl`, `opcache`, `redis`, `amqp`, `gd`, `imagick`, `xdebug`. Xdebug is installed but disabled by default (`XDEBUG_MODE=off`); see [Local Development](/frankendeploy/guides/local-development/#debugging-with-xdebug) to enable it. The `mongodb` extension is added when the [MongoDB service](#mongodb) is enabled.

### `database.driver`

| Driver | Docker image | Managed mode |
|--------|-------------|--------------|
| `pgsql` | `postgres:<version>-alpine` | Yes |
| `mysql` | `mysql:<version>` | Yes |
| `mariadb` | `mariadb:<version>` | Yes |
| `sqlite` | No container | No (file-based) |

The scanner reads `DATABASE_URL` from `.env` **and** `.env.local` (the latter wins, matching Symfony) and warns when the two disagree. MariaDB is detected from a `mariadb://` scheme or a `serverVersion` ending in `-MariaDB`; the generated URL uses the `mysql://` scheme with the `-MariaDB` suffix, as Doctrine expects.

For a **Laravel** project the scanner reads `DATABASE_URL` first, then the idiomatic `DB_CONNECTION` (`pgsql`, `mysql`, `mariadb`, `sqlite`, `mongodb`), from `.env` or `.env.example`. Unlike Symfony it does not default to PostgreSQL when nothing is configured: a Laravel app may legitimately have no database. With a managed database, FrankenDeploy injects `DB_CONNECTION`, `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME` and `DB_PASSWORD` into the container, because Laravel's `config/database.php` does not read `DATABASE_URL` by default.

### `database.managed`

| Value | Behavior |
|-------|----------|
| `true` (default for pgsql, mysql, mariadb) | FrankenDeploy creates the `<name>-db` container on the first deploy, generates random credentials, injects `DATABASE_URL` into the app, keeps the data in a Docker volume, and dumps the database before every migration |
| `false` | You provide `DATABASE_URL` with `frankendeploy env set` (external database) |

<div class="callout callout-warning">

**SQLite**

SQLite cannot run as a container: `managed: true` with `driver: sqlite` fails validation. For persistence in production, the directory of the database file is added to `shared_dirs` automatically.

</div>

### `database.path`

SQLite only. Path of the database file relative to the project root, detected from `DATABASE_URL` (`sqlite:///%kernel.project_dir%/var/data.db` gives `var/data.db`).

### `messenger`

```yaml
messenger:
  enabled: true
  transports:
    - async
    - scheduler_default
```

When enabled, each deploy (and rollback) starts one `<name>-worker` container running `php bin/console messenger:consume <transports> --time-limit=3600 --memory-limit=256M`. The worker is recreated with the new image at every deploy, so it always runs the same code as the app.

`init` fills `transports` from `config/packages/messenger.yaml` (resolving `%env()%` DSNs through `.env` / `.env.local`), excluding the failure transport and `sync://` transports. With `symfony/scheduler` installed, `scheduler_default` is added so scheduled tasks actually run in production. Every inference is announced at `init`.

### `queue` (Laravel)

```yaml
queue:
  enabled: true
  connection: redis
  queues:
    - high
    - default
```

When enabled, each deploy (and rollback) starts one `<name>-worker` container running `php artisan queue:work --tries=3 --timeout=90 --max-time=3600 --memory=256 --verbose`, with `--queue=high,default` appended when `queues` is set. `init` sets `enabled: true` when `QUEUE_CONNECTION` is anything other than `sync`, or when `laravel/horizon` is installed, and fills `connection` from the detected value. `sync` runs jobs inline and needs no worker.

### `scheduler` (Laravel)

```yaml
scheduler:
  enabled: true
```

When enabled, each deploy (and rollback) starts one `<name>-scheduler` container running `php artisan schedule:work`. `init` enables it when it finds `->withSchedule()` in `bootstrap/app.php`, `Schedule::` in `routes/console.php`, or `$schedule` in `app/Console/Kernel.php`. Without this container, tasks declared in your application never run on the server. See [Workers & Scheduled Tasks](/frankendeploy/guides/workers/#laravel).

### `mailer.enabled`

Set by `init` when `config/packages/mailer.yaml` exists. Adds a [Mailpit](https://mailpit.axllent.org/) service to the local dev environment (SMTP on `mailpit:1025`, web UI on http://localhost:8025). No effect in production.

### `mongodb`

```yaml
mongodb:
  enabled: true
  managed: false
```

MongoDB (Doctrine MongoDB ODM or the `mongodb` library) is configured in its own section, separate from `database`: no migrations, no pre-migration dumps. `init` sets `enabled: true` when it detects `doctrine/mongodb-odm`, `doctrine/mongodb-odm-bundle`, `mongodb/mongodb`, `mongodb/laravel-mongodb` or `ext-mongodb`, and adds the `mongodb` PHP extension.

- `managed: true`: FrankenDeploy provisions a `mongodb/mongodb-community-server` container as a **single-node replica set** (rs0), which enables transactions; a single node provides no data redundancy. It injects `MONGODB_URI` into the app and the Messenger worker.
- `managed: false` (default): you provide an external `MONGODB_URI`, for example an Atlas cluster (a real replica set), with `frankendeploy env set`.

In dev, `compose.yaml` also runs a `mongodb` service as a **single-node replica set** (an `rs0` replica set initiated once), on port 27017. Point your dev `MONGODB_URI` at `mongodb:27017` with the `replicaSet=rs0` parameter, e.g. `mongodb://app:app@mongodb:27017/app?authSource=admin&replicaSet=rs0`.

### `dockerfile`

```yaml
dockerfile:
  extra_packages:     # APT packages installed in the base stage
    - imagemagick
  extra_commands:     # Raw Dockerfile instructions appended to the base stage
    - "RUN install-php-extensions imagick"
```

`extra_commands` are validated: only `RUN`, `ENV`, `ARG`, `COPY`, `WORKDIR` and a few other instructions are accepted, and shell injection patterns are rejected.

### `assets`

| `build_tool` | Detected from | What the image build runs |
|------|-----------|-----|
| `npm` | `package-lock.json` | Node stage: `npm ci`, then `build_command` |
| `yarn` | `yarn.lock` | Node stage: `npm ci`, then `build_command` (see note) |
| `pnpm` | `pnpm-lock.yaml` | Node stage: `npm ci`, then `build_command` (see note) |
| `assetmapper` | `importmap.php` | `php bin/console asset-map:compile` in the PHP image, no Node stage |

With `npm`, `yarn` and `pnpm`, the assets are built in a separate Node stage (`node_version`, default 22) and the `output_dir` directory is copied into the final image. **Note**: the Node stage currently installs dependencies with `npm ci` whatever the detected tool, so a project that only has a `yarn.lock` or `pnpm-lock.yaml` needs a `package-lock.json` as well (`npm install --package-lock-only`) until the stage honors the other package managers. With AssetMapper and `symfonycasts/tailwind-bundle`, `tailwind: true` (auto-detected) runs `tailwind:build --minify` first, otherwise the site deploys without CSS.

### `deploy.domain`

The public domain Caddy serves with an automatic Let's Encrypt certificate. Optional: without it, the deploy succeeds and the app runs on the server, but nothing exposes it publicly. The domain's A record must point to the server; `frankendeploy doctor` checks it.

### `deploy.healthcheck_*`

`healthcheck_path` is used both by the deploy-time health check (before traffic switches) and by the Docker `HEALTHCHECK` baked into the image. Default `/`, or `/api` when `init` detects API Platform (a pure API answers 404 on `/`). The window is 90 seconds by default: a cold Symfony container needs time for opcache warmup and database wait. See [Health checks](/frankendeploy/guides/deployment/#health-checks).

### `deploy.keep_releases`

Number of releases kept on the server for rollback (default 5). The same count drives the Docker images kept and the pre-migration database backups.

### `deploy.shared_files` / `deploy.shared_dirs`

Paths persisted across releases, stored under `/opt/frankendeploy/apps/<name>/shared/` and mounted into the container. Defaults: `.env.local` (mounted read-only) and `var/log`, `var/sessions`. Add `public/uploads` or any directory that must survive a deploy. Setting either list replaces the default.

For a **Laravel** project, `init` sets `shared_dirs` to `storage/app`, `storage/framework/cache`, `storage/framework/sessions` and `storage/logs`, and `shared_files` to `.env`. `bootstrap/cache` and `storage/framework/views` are deliberately **not** shared: they hold the config, route and view caches, which must come from the new image or a release would boot on the previous release's cached configuration. With SQLite, the database **file** is shared (not the `database/` directory, which holds `database/migrations`).

A shared directory hides the directory of the same name in the image. So that files shipped with the project are not lost (the Symfony Demo commits its SQLite database in `data/`), the first deploy fills an **empty** shared directory with its files **committed to Git**, copied from the image. Files ignored by Git or modified locally are never copied, and a shared directory that already holds anything is never touched.

### `deploy.memory_limit` / `deploy.cpu_limit`

Optional caps on the app container (`docker run --memory` / `--cpus`), applied from the next deploy. They protect the VPS from a leaking or runaway application. Log rotation is always on for every container FrankenDeploy starts (`json-file`, 10 MB × 3 files).

### `deploy.hooks`

Commands run inside the container with `docker exec`: Symfony console, Composer, any installed binary.

- `pre_deploy` runs in the **new** container while the old one still serves traffic. A failure removes the new container and aborts the deploy (unless `--force`). With a managed database, a hook containing `doctrine:migrations:migrate` triggers an automatic backup first.
- `post_deploy` runs in the live container after the switch. A failure only warns.

`init` adds the migration hook when Doctrine is detected. For a Laravel project it adds `php artisan migrate --force` when a database is configured: the `--force` is mandatory in production, otherwise Artisan waits for a confirmation that never comes. No `cache:warmup` hook is needed: the Symfony cache is warmed at image build time, and Laravel runs `php artisan optimize` at container start.

### `env`

`env.dev` is written into the generated `compose.yaml`; `env.prod` into `compose.prod.yaml`, a template for people who deploy with Compose by hand.

**`frankendeploy deploy` does not read `env.prod`.** In production, the container gets `SERVER_NAME`, the framework's environment values (`APP_ENV=prod`, `APP_DEBUG=0` for Symfony; `APP_ENV=production`, `APP_DEBUG=false` for Laravel), the managed database variables and the trusted proxies from FrankenDeploy, and everything else from the `.env.local` on the server, managed with [`frankendeploy env`](/frankendeploy/guides/environment-variables/). Secrets never belong in `frankendeploy.yaml`, which is versioned.

A Laravel app additionally needs `APP_KEY` (the encryption key, in the `base64:` format `php artisan key:generate` produces). The deploy preflight asks for it and can generate it for you:

```bash
frankendeploy env set prod APP_KEY="base64:$(openssl rand -base64 32)"
```

Note that Laravel does not read `SYMFONY_TRUSTED_PROXIES` or `TRUSTED_PROXIES` by default: FrankenDeploy injects `TRUSTED_PROXIES`, and your app must wire it in `bootstrap/app.php` to see the real client IP behind Caddy.

## Validation

The configuration is validated on every load (`build`, `deploy`, `dev`, …). To check it without doing anything else:

```bash
frankendeploy build
```

Errors name the field and explain how to fix it.
