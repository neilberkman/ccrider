# Resume Prompt Customization

When you resume a session using `ccrider tui` (press 'r' on a session), ccrider sends a contextual prompt to Claude Code to help set the right context.

## How It Works

1. **ccrider resumes from the original project directory** - This is where Claude Code stores session files (in `~/.claude/projects/`)
2. **A prompt is sent to Claude** telling it:
   - Where you were actually working (last working directory from the session)
   - How long the session has been inactive
   - To check the current state before proceeding

This solves the problem of sessions that moved between directories (like git worktrees) - Claude can find the session files AND knows where to navigate.

## Default Prompt Template

The default template is:

```
Resuming session from {{last_updated}}. You were last working in: {{last_cwd}}

IMPORTANT: This session has been inactive for {{time_since}}. Before proceeding: check git status, look around to understand what changed, and be careful not to overwrite any work in progress.

First, navigate to where you left off.
```

## Available Variables

You can use these variables in your custom template:

- `{{last_updated}}` - Exact timestamp when session was last active (e.g., "2025-11-08T22:29:11.424Z")
- `{{last_cwd}}` - Last working directory from the session (e.g., "/Users/you/project/.worktrees/feature")
- `{{time_since}}` - Human-readable time ago (e.g., "6 hours ago", "2 days ago")
- `{{project_path}}` - Original project directory where session started (e.g., "/Users/you/project")

## Customizing the Prompt

Create a custom template file at `~/.config/ccrider/resume_prompt.txt`:

```bash
mkdir -p ~/.config/ccrider
cat > ~/.config/ccrider/resume_prompt.txt << 'EOF'
Session resumed! Last active: {{time_since}}

You were in: {{last_cwd}}

Quick context refresh:
- Run git status and git log
- Check for uncommitted changes
- Look for any WIP or TODO comments

Let's pick up where we left off.
EOF
```

The template uses Mustache syntax (`{{variable}}`). ccrider will automatically load your custom template instead of the default.

## Testing Your Template

Use the `debug-prompt` command to see what prompt will be generated for a session:

```bash
ccrider debug-prompt <session-id>
```

This shows:

- The session info (paths, timestamps)
- The template variables and their values
- The final rendered prompt
- The exact command that will be run

## Examples

### Minimal prompt:

```
Back in session from {{time_since}} ago. You were in {{last_cwd}}
```

### Detailed prompt with checklist:

```
Resuming session: {{last_cwd}}
Last active: {{last_updated}} ({{time_since}})

Before continuing:
1. git status - check for uncommitted changes
2. git log -5 - review recent commits
3. ls - verify current directory state
4. Look for .md files with session notes

Context set. Ready to continue.
```

### Worktree-aware prompt:

```
Session active {{time_since}}.

Original: {{project_path}}
Last dir: {{last_cwd}}

If this is a worktree: check git branch and verify you're on the right branch before making changes.
```
