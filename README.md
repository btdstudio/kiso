# kiso

基礎, "foundation". The shared core behind [zumen](https://github.com/btdstudio/zumen)
and [tomo](https://github.com/btdstudio/tomo): the parts of a folder-driven
Interpretable Context Methodology (ICM) tool that don't depend on any one tool's
phases, folders or personas. Each tool passes in its own configuration.

| Package | Purpose |
|---|---|
| `scaffold` | Copy an embedded file tree into a workspace, refresh it without overwriting user edits, and validate it |
| `state` | A generic phase machine plus a committed `STATE.json` and `LOG.md` |
| `repos` | A registry of logical repos: committed `REPOS.json`, local `.<tool>/paths.json`, and the `repo` and `env` commands |
| `frontmatter` | Read and edit a controlled YAML subset at the top of a markdown file, keeping unchanged fields byte for byte |
| `ticket` | Ticket schema v1: parse, validate, hash, freeze, dependency order |
| `settings` | Merge settings: defaults ← global (`~/.config/<tool>/`) ← project |
| `workspace` | The local `.<tool>/` directory and the active-change pointer |
| `changelog` | Append timestamped lines to a log file |
| `version` | Report the running binary's version from build info |

Runtime: Go >= 1.22, standard library only.

## Core settings

kiso validates one setting itself, for every tool: `vcs`, which says whether a
workspace lives in git. Like any setting, the project value supersedes the global
one.

| Value | Meaning |
|---|---|
| `git` (default) | Transitions commit `STATE.json` and `LOG.md`. `init` runs `git init`. Git files are scaffolded. |
| `none` | The workspace is plain local files. Transitions never run git or warn. `init` skips `git init`, records `"vcs": "none"` in the project settings, and leaves out `.gitattributes` and `.gitkeep`, while still creating their folders. It keeps `.gitignore`, because that file keeps the machine-local `.<tool>/active` and `.<tool>/paths.json` out of an enclosing git repo, or one created later. `update` keeps `.gitignore` managed like any other file, and neither writes nor reports `.gitattributes` or `.gitkeep`, dropping them from the manifest without deleting them from disk. |

Any other value fails `settings.Load`. `Loaded.VCS` holds the effective value. A
tool passes it to `state.Store.VCS` and `scaffold.Engine.VCS`. When a tool adopts
a kiso version with `vcs`, it should wire both fields in the same change, because
the "not in a git repo" warning now suggests setting `"vcs": "none"`.
