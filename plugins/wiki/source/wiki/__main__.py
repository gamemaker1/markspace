"""Run the Bookmark wiki plugin as an HTTP service."""

from __future__ import annotations

import logging
import os


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"),
                        format="%(asctime)s %(levelname)s %(name)s %(message)s")
    from .app import create_plugin

    plugin = create_plugin()
    plugin.serve(host=os.environ.get("HOST", "0.0.0.0"), port=int(os.environ.get("PORT", "8090")))


if __name__ == "__main__":
    main()
