# Configuration

ccrider can be configured via files in `~/.config/ccrider/`.

## Config Files

### config.toml

Main configuration file for global settings.

**Location**: `~/.config/ccrider/config.toml`

**Example**:

```toml
# Additional flags to pass to every claude --resume command
claude_flags = ["--dangerously-skip-permissions"]

# Additional flags to pass to every codex resume command
# (placed before the subcommand: codex <flags> resume <id>)
# codex_flags = ["--dangerously-bypass-approvals-and-sandbox"]

# Additional flags to pass to every copilot --resume command
# copilot_flags = []
```

### resume_prompt.txt

Custom template for session resume prompts (uses Mustache syntax).

**Location**: `~/.config/ccrider/resume_prompt.txt`

See [RESUME_PROMPT.md](RESUME_PROMPT.md) for details.

### terminal_command.txt

Custom command for spawning terminal windows.

**Location**: `~/.config/ccrider/terminal_command.txt`

**Example**:

```bash
osascript -e 'tell application "iTerm" to create window with default profile command "cd {cwd} && {command}"'
```

Template variables:

- `{cwd}` - Working directory
- `{command}` - Command to execute

## Configuration Options

### claude_flags / codex_flags / copilot_flags

**Type**: array of strings
**Default**: `[]`
**File**: `config.toml`

Extra flags injected into every resume command ccrider builds for that provider — the TUI launch (`r`), copy-to-clipboard (`c`), write-to-file (`w`), new-terminal (`o`), `debug-prompt`, and the `resume_command` field in MCP tool responses.

Each provider's flags go where its CLI expects global options:

| Key             | Resulting command                  |
| --------------- | ---------------------------------- |
| `claude_flags`  | `claude <flags> --resume <id>`     |
| `codex_flags`   | `codex <flags> resume <id>`        |
| `copilot_flags` | `copilot <flags> --resume=<id>`    |

Codex flags are placed **before** the subcommand (`codex [OPTIONS] <COMMAND>`); codex rejects global options after it. Forking keeps the same placement (`codex <flags> fork <id>`, `claude <flags> --resume <id> --fork-session`).

**WARNING**: Flags like `--dangerously-skip-permissions` (Claude) and `--dangerously-bypass-approvals-and-sandbox` (Codex) bypass safety checks. Only configure them in trusted, personal environments.

**Example config**:

```toml
# ~/.config/ccrider/config.toml
claude_flags = ["--dangerously-skip-permissions"]
codex_flags = ["--dangerously-bypass-approvals-and-sandbox"]
# copilot_flags = []
```

## Configuration Loading Order

1. Load default values
2. Load `config.toml` if present
3. Load `resume_prompt.txt` if present
4. Load `terminal_command.txt` if present

Later values override earlier ones.

## Creating Config Directory

```bash
mkdir -p ~/.config/ccrider
```

## Example Configurations

### Minimal (defaults only)

No config files needed - ccrider works out of the box.

### Custom Resume Prompt

```bash
# ~/.config/ccrider/resume_prompt.txt
Back in session from {{time_since}}.
{{#different_directory}}
Started in: {{last_cwd}}
{{/different_directory}}

Check git status before continuing.
```

### Personal Dev Machine (skip permission prompts on resume)

```toml
# ~/.config/ccrider/config.toml
claude_flags = ["--dangerously-skip-permissions"]
codex_flags = ["--dangerously-bypass-approvals-and-sandbox"]
```

### Custom Terminal (iTerm2)

```bash
# ~/.config/ccrider/terminal_command.txt
osascript -e 'tell application "iTerm" to create window with default profile command "cd {cwd} && {command}"'
```
