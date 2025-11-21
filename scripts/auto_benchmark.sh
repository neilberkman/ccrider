#!/bin/bash
# Automatically benchmark different context window sizes

set -e

DB_PATH="${HOME}/.config/ccrider/sessions.db"
RESULTS="/tmp/llm_benchmark_$(date +%Y%m%d_%H%M%S).csv"
INFERENCE_FILE="internal/core/llm/inference.go"

# Context sizes to test
CONTEXT_SIZES=(4096 8192 16384 32768)

echo "context_size,session_id,msg_count,real_sec,user_sec,sys_sec,mem_mb,success" > "$RESULTS"

# Get test sessions (varying sizes, not too big)
TEST_SESSIONS=$(sqlite3 "$DB_PATH" "
SELECT session_id, message_count
FROM sessions
WHERE message_count BETWEEN 50 AND 150
ORDER BY RANDOM()
LIMIT 3
")

echo "=== LLM Context Benchmark ==="
echo "Test sessions:"
echo "$TEST_SESSIONS"
echo ""
echo "Results: $RESULTS"
echo ""

# Backup original file
cp "$INFERENCE_FILE" "${INFERENCE_FILE}.backup"

for ctx_size in "${CONTEXT_SIZES[@]}"; do
    echo "Testing context size: ${ctx_size}..."

    # Modify inference.go
    sed -i.tmp "s/llama.WithContext([0-9]*)/llama.WithContext(${ctx_size})/" "$INFERENCE_FILE"
    rm -f "${INFERENCE_FILE}.tmp"

    # Rebuild
    echo "  Building..."
    go build -o ccrider ./cmd/ccrider 2>&1 | grep -v "ignoring duplicate" || true
    install_name_tool -add_rpath /opt/homebrew/lib ./ccrider 2>&1 | grep -v "already exists" || true

    # Test each session
    while IFS='|' read -r session_id msg_count; do
        echo "  Testing session ${session_id} (${msg_count} msgs)..."

        # Run with timing
        OUTPUT=$(/usr/bin/time -l ./ccrider summarize --session "$session_id" --model llama-8b 2>&1)

        # Extract metrics
        REAL=$(echo "$OUTPUT" | grep "real" | awk '{print $1}' | tr -d 's')
        USER=$(echo "$OUTPUT" | grep "user" | awk '{print $1}' | tr -d 's')
        SYS=$(echo "$OUTPUT" | grep "sys" | awk '{print $1}' | tr -d 's')
        MEM=$(echo "$OUTPUT" | grep "maximum resident" | awk '{printf "%.0f", $1/1024/1024}')

        # Check if successful
        if echo "$OUTPUT" | grep -q "✓"; then
            SUCCESS="true"
        else
            SUCCESS="false"
        fi

        echo "${ctx_size},${session_id},${msg_count},${REAL},${USER},${SYS},${MEM},${SUCCESS}" >> "$RESULTS"
        echo "    Time: ${REAL}s, Memory: ${MEM}MB, Success: ${SUCCESS}"

        # Cool down between tests
        sleep 2
    done <<< "$TEST_SESSIONS"

    echo ""
done

# Restore original
mv "${INFERENCE_FILE}.backup" "$INFERENCE_FILE"
echo "Restored original inference.go"
echo ""
echo "=== Benchmark Complete ==="
echo "Results: $RESULTS"
echo ""
echo "Summary:"
column -t -s',' "$RESULTS"
