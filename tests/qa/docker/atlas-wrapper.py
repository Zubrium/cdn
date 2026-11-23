#!/usr/bin/env python3
"""Run the pinned Atlas image against disposable host-side migration inputs."""

import os
import pathlib
import subprocess
import sys
from urllib.parse import urlparse


IMAGE = "arigaio/atlas:1.3.0"


def main() -> int:
    args = sys.argv[1:]
    if args == ["version"]:
        command = ["docker", "run", "--rm", IMAGE, "version"]
        result = subprocess.run(command, capture_output=True, text=True, check=False)
        for line in result.stdout.splitlines():
            if line.startswith("atlas ") and " version " in line:
                print(line)
                break
        if result.returncode:
            sys.stderr.write(result.stderr)
        return result.returncode

    try:
        config_index = args.index("--config")
        config_arg = args[config_index + 1]
    except (ValueError, IndexError):
        print("atlas wrapper requires --config", file=sys.stderr)
        return 2

    stage = pathlib.Path(urlparse(config_arg).path).parent
    if not stage.is_dir():
        print("staged Atlas directory does not exist", file=sys.stderr)
        return 2

    rewritten = []
    for arg in args:
        parsed = urlparse(arg)
        if parsed.scheme == "file":
            rewritten.append("file:///atlas-stage/" + pathlib.Path(parsed.path).name)
        else:
            rewritten.append(arg)

    command = [
        "docker",
        "run",
        "--rm",
        "--add-host=host.docker.internal:host-gateway",
        "-v",
        f"{stage}:/atlas-stage:ro",
    ]
    for key in (
        "PATH",
        "HOME",
        "TMPDIR",
        "SSL_CERT_FILE",
        "SSL_CERT_DIR",
        "ATLAS_NO_UPDATE_NOTIFIER",
        "ZUBRIUM_CORE_DATABASE_URL",
        "ZUBRIUM_CORE_MIGRATION_DATABASE_URL",
        "ZUBRIUM_CORE_MIGRATION_DIR",
    ):
        value = os.environ.get(key)
        if value is None:
            continue
        if key.endswith("_DATABASE_URL"):
            value = value.replace("@127.0.0.1:", "@host.docker.internal:")
        if key.endswith("_DIR") and value.startswith("file://"):
            value = "file:///atlas-stage/" + pathlib.Path(urlparse(value).path).name
        command.extend(["-e", f"{key}={value}"])
    command.append(IMAGE)
    command.extend(rewritten)
    return subprocess.call(command)


if __name__ == "__main__":
    raise SystemExit(main())
