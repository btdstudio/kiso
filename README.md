# kiso

基礎, "foundation". The shared core behind [zumen](https://github.com/btdstudio/zumen)
and [tomo](https://github.com/btdstudio/tomo): the parts of a folder-driven
Interpretable Context Methodology (ICM) tool that don't depend on any one tool's
phases, folders or personas. Each tool passes in its own configuration.

| Package | Purpose |
|---|---|
| `scaffold` | Copy an embedded file tree into a workspace, refresh it without overwriting user edits, and validate it |
| `settings` | Merge settings: defaults ← global (`~/.config/<tool>/`) ← project |
| `workspace` | The local `.<tool>/` directory and the active-change pointer |
| `changelog` | Append timestamped lines to a log file |
| `version` | Report the running binary's version from build info |

Runtime: Go >= 1.22, standard library only.
