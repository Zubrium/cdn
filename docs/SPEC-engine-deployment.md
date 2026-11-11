# Manual Engine deployment

## Goal and scope

The `Deploy Engine` GitHub Actions workflow manually deploys an already published GHCR image to one Debian or Ubuntu host. It installs Docker Engine and the Compose plugin, copies the repository's Compose file and Chromium seccomp profile, writes the configured runtime environment with mode `0600`, pulls the chosen image, starts the existing Compose stack, and waits for `/health/ready`.

The workflow is dispatched from `main` and is bound to the GitHub Environment `production`. The operator supplies an image tag (`main`, a published SemVer tag, or a full `sha-<40 hex>` image tag). It checks that the image is available before connecting to the server. The Engine image is built and published separately by [engine-image.yml](../.github/workflows/engine-image.yml).

## State and safety invariants

- The deploy job never builds an image on the server and never deploys an untagged or user-supplied registry/repository.
- The target directory is `/opt/website-engine`; the Compose project name is `website-engine` so its named volumes remain stable between runs.
- Deployment pulls only the selected `server` image, then uses `docker compose up -d`. It does not refresh infrastructure images on ordinary Engine updates, run `down`, delete volumes, remove orphan containers, reset a database, or clean Docker globally.
- Existing Compose data volumes are preserved. Database migrations remain controlled by the Engine's configured `MIGRATE_ON_START` value.
- Configuration and secret keys arrive from separate GitHub Environment settings, are merged only in a runner-local mode-`0600` file, copied to the server as `/opt/website-engine/.env` mode `0600`, and removed from the runner in an `always()` cleanup step.
- Secrets and the `copy` task carrying `.env` are excluded from Ansible logs. SSH host key verification is strict and uses a pre-verified `DEPLOY_KNOWN_HOSTS` secret.
- The workflow supports exactly one SSH authentication method per run: `DEPLOY_SSH_KEY` or `DEPLOY_SSH_PASSWORD`. Optional `DEPLOY_BECOME_PASSWORD` is used if sudo requires a password.
- The playbook supports Debian and Ubuntu, installs Docker from Docker's official APT repository, enables the daemon at boot, and leaves reverse proxy/TLS configuration to the existing network layer.
- Deployment is serialized; concurrent production deploys do not run at the same time.

## GitHub Environment configuration

Create/configure the `production` Environment in repository settings.

**Environment Variables**

- `DEPLOY_HOST`: `system.onix.fun` (server DNS name or IP).
- `DEPLOY_USER`: `docup` (SSH user with sudo access).
- `DEPLOY_PORT`: `22` (optional; defaults to `22`).
- `ENGINE_ENV_CONFIG`: multiline dotenv entries copied from `.env.example`, excluding secret keys and `ENGINE_IMAGE`. Keep deployment-specific values here, including `BRANCHES`, public URLs, ports, and operational limits.

**Environment Secrets**

- `DEPLOY_KNOWN_HOSTS`: pre-verified SSH host key line(s) for the server.
- Exactly one of `DEPLOY_SSH_KEY` (private key) or `DEPLOY_SSH_PASSWORD` (SSH password).
- `DEPLOY_BECOME_PASSWORD`: optional sudo password when sudo is not passwordless.
- `ENGINE_ENV_SECRETS`: multiline dotenv entries for credentials. At minimum provide `DATABASE_URL`, `POSTGRES_PASSWORD`, `S3_ACCESS_KEY`, and `S3_SECRET_KEY`; include any other secrets referenced by the chosen Engine configuration, such as bootstrap, signing, or external file-service keys.

Use every assignment from `.env.example`, split between the two GitHub settings; the helper rejects missing sample keys, duplicate keys, sensitive keys placed in `ENGINE_ENV_CONFIG`, missing required Compose credentials, and a manually supplied `ENGINE_IMAGE`. It sets `ENGINE_IMAGE` from the repository's canonical lowercase `ghcr.io/<owner>/<repository>` path and the selected tag. On an update, Ansible keeps the previous `.env` at `/opt/website-engine/.env.previous` with mode `0600` before installing the new configuration.

The GHCR package must allow anonymous pulls from the target host. If it is private, configure a narrowly scoped read-only package credential and a remote Docker login before using this workflow; do not put a write token on the server.

## First deployment and updates

1. Verify the SSH host fingerprint out of band and add the resulting known-hosts line to the `production` Environment secret.
2. Add the host/user/port and non-secret runtime configuration as Environment Variables; add SSH and application credentials as Environment Secrets.
3. Confirm the `docup` account can run required Docker installation and Compose commands through sudo. The workflow does not change firewall, DNS, reverse proxy, or TLS settings.
4. Open **Actions → Deploy Engine → Run workflow**, keep the source ref on `main`, enter a published image tag, and review the `production` deployment record.
5. After completion, verify the readiness result and the public routes through the configured gateway.

For updates, dispatch the same workflow with the desired immutable SemVer or full SHA tag. `main` is available for an intentional rolling deployment of the latest image published from the main branch.

## References

- [Docker Engine installation on Ubuntu](https://docs.docker.com/engine/install/ubuntu/) and [Debian](https://docs.docker.com/engine/install/debian/).
- [Docker Compose plugin installation](https://docs.docker.com/compose/install/linux/).
- [GitHub Actions variables](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-variables) and [secrets](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets).
