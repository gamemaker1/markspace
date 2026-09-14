"""Health probe for plugin containers.

`python3 -m markspace.health` exits 0 when the plugin in the same container
answers GET /health on the port in the PORT environment variable, and 1
otherwise. Container health checks run it, because a quadlet HealthCmd line
cannot carry a quoted Python snippet.
"""

from __future__ import annotations

import os
import sys
import urllib.request


def main() -> int:
    port = os.environ.get("PORT", "8090")
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=3) as response:
            if response.status == 200:
                return 0
            print(f"unhealthy: status {response.status}", file=sys.stderr)
    except OSError as err:
        print(f"unhealthy: {err}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
