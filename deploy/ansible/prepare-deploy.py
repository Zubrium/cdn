#!/usr/bin/env python3
"""Create mode-0600, runner-local inputs for the manual Engine deployment."""

from __future__ import annotations

import ipaddress
import json
import os
import re
from pathlib import Path


SECRET_ENV_KEYS = {
    "DATABASE_URL",
    "POSTGRES_PASSWORD",
    "S3_ACCESS_KEY",
    "S3_SECRET_KEY",
    "BOOTSTRAP_ADMIN_TOKEN",
    "ENGINE_FILE_SIGNING_SECRET",
}


def is_sensitive_key(key: str) -> bool:
    return (
        key in SECRET_ENV_KEYS
        or key.endswith(("_PASSWORD", "_SECRET", "_TOKEN", "_ACCESS_KEY", "_API_KEY"))
        or key.endswith("_KEY")
    )


def fail(message: str) -> None:
    raise SystemExit(message)


def parse_env(text: str, label: str) -> tuple[list[str], dict[str, str]]:
    lines: list[str] = []
    values: dict[str, str] = {}
    for number, raw in enumerate(text.splitlines(), start=1):
        stripped = raw.strip()
        if not stripped or stripped.startswith("#"):
            lines.append(raw)
            continue
        if "=" not in raw:
            fail(f"{label}:{number} must use KEY=VALUE syntax")
        key, value = raw.split("=", 1)
        key = key.strip()
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", key):
            fail(f"{label}:{number} has an invalid environment variable name")
        if key in values:
            fail(f"{label} contains duplicate key {key}")
        values[key] = value
        lines.append(raw)
    return lines, values


def write_private(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o600)


def main() -> None:
    runner_temp = Path(os.environ["RUNNER_TEMP"]).resolve()
    work_dir = runner_temp / "engine-deploy"
    work_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    work_dir.chmod(0o700)

    host = os.environ.get("DEPLOY_HOST", "").strip()
    user = os.environ.get("DEPLOY_USER", "").strip()
    port_text = os.environ.get("DEPLOY_PORT", "22").strip() or "22"
    version = os.environ.get("ENGINE_IMAGE_TAG", "").strip()
    known_hosts = os.environ.get("DEPLOY_KNOWN_HOSTS", "").strip()
    private_key = os.environ.get("DEPLOY_SSH_KEY", "")
    ssh_password = os.environ.get("DEPLOY_SSH_PASSWORD", "")
    become_password = os.environ.get("DEPLOY_BECOME_PASSWORD", "")
    config_text = os.environ.get("ENGINE_ENV_CONFIG", "")
    secrets_text = os.environ.get("ENGINE_ENV_SECRETS", "")

    if not host or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.:-]*", host):
        fail("Set a valid DEPLOY_HOST environment variable.")
    try:
        ipaddress.ip_address(host)
    except ValueError:
        if len(host) > 253 or any(
            not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?", label)
            for label in host.split(".")
        ):
            fail("DEPLOY_HOST must be an IP address or DNS hostname.")
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_.-]*[$]?", user):
        fail("Set a valid DEPLOY_USER environment variable.")
    if not port_text.isdigit() or not 1 <= int(port_text) <= 65535:
        fail("DEPLOY_PORT must be between 1 and 65535.")
    if not re.fullmatch(r"(?:main|[0-9]+\.[0-9]+\.[0-9]+|sha-[0-9a-f]{40})", version):
        fail("ENGINE_IMAGE_TAG must be main, a published SemVer tag, or sha-<40 lowercase hex characters>.")
    if not known_hosts:
        fail("Set DEPLOY_KNOWN_HOSTS to a verified SSH known_hosts entry.")
    if bool(private_key.strip()) == bool(ssh_password.strip()):
        fail("Configure exactly one of DEPLOY_SSH_KEY or DEPLOY_SSH_PASSWORD.")
    if not config_text.strip() or not secrets_text.strip():
        fail("Set both ENGINE_ENV_CONFIG (GitHub Variable) and ENGINE_ENV_SECRETS (GitHub Secret).")

    config_lines, config = parse_env(config_text, "ENGINE_ENV_CONFIG")
    secret_lines, secrets = parse_env(secrets_text, "ENGINE_ENV_SECRETS")
    overlap = sorted(set(config) & set(secrets))
    if overlap:
        fail(f"Environment keys appear in both config and secrets: {', '.join(overlap)}")
    misplaced = sorted(key for key in config if is_sensitive_key(key))
    if misplaced:
        fail(f"Move sensitive keys from ENGINE_ENV_CONFIG to ENGINE_ENV_SECRETS: {', '.join(misplaced)}")
    required_secrets = {"DATABASE_URL", "POSTGRES_PASSWORD", "S3_ACCESS_KEY", "S3_SECRET_KEY"}
    missing = sorted(required_secrets - set(secrets))
    if missing:
        fail(f"ENGINE_ENV_SECRETS is missing required keys: {', '.join(missing)}")
    empty_required = sorted(key for key in required_secrets if not secrets.get(key, "").strip())
    if empty_required:
        fail(f"ENGINE_ENV_SECRETS has empty required values: {', '.join(empty_required)}")
    placeholders = sorted(
        key
        for key, value in (config | secrets).items()
        if "replace-with-" in value.lower()
    )
    if placeholders:
        fail(f"Replace sample placeholder values before deployment: {', '.join(placeholders)}")
    if "ENGINE_IMAGE" in config or "ENGINE_IMAGE" in secrets:
        fail("Do not set ENGINE_IMAGE in GitHub environment data; the selected image tag supplies it.")
    env_example = Path(__file__).resolve().parents[2] / ".env.example"
    _, example_values = parse_env(env_example.read_text(encoding="utf-8"), ".env.example")
    missing_config = sorted(set(example_values) - {"ENGINE_IMAGE"} - set(config) - set(secrets))
    if missing_config:
        fail(f"Combined GitHub environment data is missing .env.example keys: {', '.join(missing_config)}")

    env_content = "\n".join(config_lines + secret_lines + [f"ENGINE_IMAGE=ghcr.io/onix-fun/website-engine:{version}", ""])
    env_path = work_dir / "engine.env"
    write_private(env_path, env_content)

    known_hosts_path = work_dir / "known_hosts"
    write_private(known_hosts_path, known_hosts + "\n")

    host_vars: dict[str, object] = {
        "ansible_host": host,
        "ansible_user": user,
        "ansible_port": int(port_text),
        "ansible_ssh_common_args": (
            f"-o UserKnownHostsFile={known_hosts_path} -o StrictHostKeyChecking=yes"
        ),
    }
    key_path = ""
    if private_key.strip():
        key_path = str(work_dir / "id_deploy")
        write_private(Path(key_path), private_key.rstrip() + "\n")
        host_vars["ansible_ssh_private_key_file"] = key_path

    inventory_path = work_dir / "inventory.yml"
    write_private(
        inventory_path,
        json.dumps({"all": {"children": {"engine": {"hosts": {"engine": host_vars}}}}}, indent=2) + "\n",
    )

    connection_vars: dict[str, str] = {}
    if ssh_password.strip():
        connection_vars["ansible_password"] = ssh_password
    if become_password.strip():
        connection_vars["ansible_become_password"] = become_password
    connection_path = work_dir / "connection.json"
    if connection_vars:
        write_private(connection_path, json.dumps(connection_vars) + "\n")

    outputs = {
        "work_dir": str(work_dir),
        "env_file": str(env_path),
        "inventory": str(inventory_path),
        "connection_file": str(connection_path) if connection_vars else "",
        "auth_method": "password" if ssh_password.strip() else "key",
    }
    if key_path:
        outputs["private_key"] = key_path
    with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as output:
        for key, value in outputs.items():
            output.write(f"{key}={value}\n")


if __name__ == "__main__":
    main()
