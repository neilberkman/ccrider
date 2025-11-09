# Configuration

ccrider can be configured via files in `~/.config/ccrider/`.

## Config Files

### config.toml

Main configuration file for global settings.

**Location**: `~/.config/ccrider/config.toml`

**Example**:

```toml
# Skip all permission prompts
# WARNING: This disables safety checks. Use only in trusted environments.
dangerously_skip_permissions = true
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

### dangerously_skip_permissions

**Type**: boolean
**Default**: `false`
**File**: `config.toml`

Skips all permission prompts when running MCP server tools or other operations that might require confirmation.

**WARNING**: This setting bypasses safety checks. Only enable in trusted, personal environments where you fully control the data and operations.

**Use cases**:

- Personal development machine where you trust all operations
- Automated workflows that need non-interactive operation
- Testing and development

**Not recommended for**:

- Shared machines
- Production environments
- When working with untrusted session data

**Example config**:

```toml
# ~/.config/ccrider/config.toml
dangerously_skip_permissions = true
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

### Personal Dev Machine (skip permissions)

```toml
# ~/.config/ccrider/config.toml
dangerously_skip_permissions = true
```

### Custom Terminal (iTerm2)

```bash
# ~/.config/ccrider/terminal_command.txt
osascript -e 'tell application "iTerm" to create window with default profile command "cd {cwd} && {command}"'
```
