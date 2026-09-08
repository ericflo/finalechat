#!/usr/bin/env python3
"""Embed maintainable integration sources/assets into the standalone CLI."""
import argparse
import base64
import gzip
import hashlib
import json
import os
from pathlib import Path
import tempfile

ROOT = Path(__file__).resolve().parents[1]
START = "# BEGIN GENERATED INTEGRATIONS (scripts/build-cli-integrations.py)"
END = "# END GENERATED INTEGRATIONS"


def build():
    assets = {"sdk": ROOT / "sdk/finale-artifact.js", "viewer": ROOT / "cli/assets/viewer.html",
              "settings": ROOT / "cli/assets/settings.html"}
    encoded = {name: base64.b64encode(gzip.compress(path.read_bytes(), mtime=0)).decode() for name, path in assets.items()}
    digest = hashlib.sha256(json.dumps(encoded, sort_keys=True).encode()).hexdigest()
    source = (ROOT / "cli/integrations.py").read_text()
    adapter_digest = hashlib.sha256(source.encode()).hexdigest()
    block = START + "\nINTEGRATION_ASSETS = " + repr(encoded) + "\nINTEGRATION_ASSETS_DIGEST = " + repr(digest) + "\nINTEGRATION_ADAPTER_DIGEST = " + repr(adapter_digest) + "\n" + source + "\n" + END
    path = ROOT / "cli/finalechat"
    current = path.read_text()
    if START in current:
        start, end = current.index(START), current.index(END) + len(END)
        result = current[:start] + block + current[end:]
    else:
        marker = "# --------------------------------------------------------------------------- #\n# Argument parsing"
        result = current.replace(marker, block + "\n\n\n" + marker)
    return path, current, result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    path, current, result = build()
    if args.check:
        if current != result:
            raise SystemExit("Run python3 scripts/build-cli-integrations.py to synchronize the single-file CLI.")
    elif current != result:
        # Readers include the Go embed compiler and a running local server.
        # Never truncate their executable while publishing a generated build.
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".finalechat-build-", delete=False) as output:
            temporary = Path(output.name)
            try:
                os.fchmod(output.fileno(), path.stat().st_mode & 0o777)
                output.write(result.encode())
                output.flush()
                os.fsync(output.fileno())
            except BaseException:
                temporary.unlink(missing_ok=True)
                raise
        try:
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)
