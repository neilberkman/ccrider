#!/bin/bash
# Sets up fake Claude Code sessions for testing

CLAUDE_DIR="$HOME/.claude/projects/-Users-test-project"
mkdir -p "$CLAUDE_DIR"

# Create a fake session
SESSION_ID="test-session-$(date +%s)"
cat > "$CLAUDE_DIR/$SESSION_ID.jsonl" <<'EOF'
{"type":"session_start","session_id":"test-session-123","timestamp":"2025-11-09T12:00:00Z","project_path":"/Users/test/project","cwd":"/Users/test/project"}
{"type":"user","content":"Hello, can you help me fix this authentication bug?","timestamp":"2025-11-09T12:00:01Z","sequence":1,"cwd":"/Users/test/project"}
{"type":"assistant","content":"Of course! I'd be happy to help you debug the authentication issue. Could you share the relevant code and describe what's happening?","timestamp":"2025-11-09T12:00:05Z","sequence":2}
{"type":"user","content":"The login endpoint returns 401 even with valid credentials","timestamp":"2025-11-09T12:00:30Z","sequence":3,"cwd":"/Users/test/project"}
{"type":"assistant","content":"Let me check the authentication middleware. It looks like the password comparison might be case-sensitive. Try using bcrypt.CompareHashAndPassword instead.","timestamp":"2025-11-09T12:01:00Z","sequence":4}
EOF

# Create another session
SESSION_ID2="test-session-$(date +%s)-2"
cat > "$CLAUDE_DIR/$SESSION_ID2.jsonl" <<'EOF'
{"type":"session_start","session_id":"test-session-456","timestamp":"2025-11-08T10:00:00Z","project_path":"/Users/test/project","cwd":"/Users/test/project"}
{"type":"user","content":"How do I implement rate limiting in Go?","timestamp":"2025-11-08T10:00:01Z","sequence":1,"cwd":"/Users/test/project"}
{"type":"assistant","content":"For rate limiting in Go, I recommend using golang.org/x/time/rate. Here's a basic example...","timestamp":"2025-11-08T10:00:05Z","sequence":2}
EOF

echo "Created fake sessions in $CLAUDE_DIR"
echo "Files:"
ls -lh "$CLAUDE_DIR"

echo ""
echo "Now run:"
echo "  ./ccrider sync"
echo "  ./ccrider tui"
