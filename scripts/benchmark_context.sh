#!/bin/bash
# Benchmark different context window sizes for LLM summarization

set -e

DB_PATH="${HOME}/.config/ccrider/sessions.db"
RESULTS_FILE="/tmp/context_benchmark_$(date +%s).txt"

echo "=== LLM Context Window Benchmark ===" | tee "$RESULTS_FILE"
echo "Started: $(date)" | tee -a "$RESULTS_FILE"
echo "" | tee -a "$RESULTS_FILE"

# Get a few test sessions of varying sizes
echo "Selecting test sessions..." | tee -a "$RESULTS_FILE"
sqlite3 "$DB_PATH" <<EOF | tee -a "$RESULTS_FILE"
SELECT
    session_id,
    message_count,
    printf('%.1f KB', LENGTH(GROUP_CONCAT(text_content, ' ')) / 1024.0) as approx_size
FROM sessions s
JOIN messages m ON s.id = m.session_id
WHERE message_count BETWEEN 50 AND 200
GROUP BY s.session_id
ORDER BY message_count
LIMIT 5;
EOF

echo "" | tee -a "$RESULTS_FILE"
echo "Test sessions selected. Ready to benchmark." | tee -a "$RESULTS_FILE"
echo "" | tee -a "$RESULTS_FILE"
echo "Results will be saved to: $RESULTS_FILE"
echo ""
echo "Press Enter to start benchmarking..."
read

# We'll manually test each context size
echo "Instructions:" | tee -a "$RESULTS_FILE"
echo "1. Edit internal/core/llm/inference.go" | tee -a "$RESULTS_FILE"
echo "2. Change llama.WithContext(XXXX) to test value" | tee -a "$RESULTS_FILE"
echo "3. Rebuild: go build -o ccrider ./cmd/ccrider" | tee -a "$RESULTS_FILE"
echo "4. Run test command for each session" | tee -a "$RESULTS_FILE"
echo "" | tee -a "$RESULTS_FILE"

cat << 'BENCHMARK_SCRIPT' | tee -a "$RESULTS_FILE"

# For each context size (8192, 16384, 32768, 65536):

# Test command (example for session_id):
time (
  /usr/bin/time -l ./ccrider summarize --session SESSION_ID --model llama-8b 2>&1 | \
    grep -E "(maximum resident|real|user|sys)"
)

# Record:
# - Context size
# - Session ID
# - Message count
# - Peak memory (maximum resident set size)
# - Real time
# - Quality of summary (subjective 1-5)

BENCHMARK_SCRIPT

echo ""
echo "Results file: $RESULTS_FILE"
