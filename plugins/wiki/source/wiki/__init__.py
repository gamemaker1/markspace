"""Bookmark wiki, a Markspace plugin.

The plugin reads a user's bookmarks, saves the readable text of each page as a
source note, summarises it, and maintains a wiki of linked pages. It proposes
changes to notes it did not generate as suggestions. spec.md is the contract.
"""

from .manifest import MANIFEST, PLUGIN_ID, VERSION

__all__ = ["MANIFEST", "PLUGIN_ID", "VERSION", "create_plugin"]


def create_plugin(**options):
    """Build the plugin. Tests pass data_dir, env, clock and timing options."""
    from .app import create_plugin as _create

    return _create(**options)
