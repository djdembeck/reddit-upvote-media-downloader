#!/bin/sh

PASS=0
FAIL=0
TOTAL=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

pass() {
    echo "✓ PASS: $1"
    PASS=$((PASS + 1))
    TOTAL=$((TOTAL + 1))
}

fail() {
    echo "✗ FAIL: $1"
    FAIL=$((FAIL + 1))
    TOTAL=$((TOTAL + 1))
}

run_validate() {
    result=$(sh -c '. "$1/entrypoint.sh" && validate_puid_pgid '"${2:-}" _ "$SCRIPT_DIR" 2>&1)
    RUN_VALIDATE_EXIT_CODE=$?
    RUN_VALIDATE_OUTPUT="$result"
}

test_validate_puid_pgid_valid() {
    echo "Testing validate_puid_pgid with valid values..."

    export PUID=1000
    export PGID=1000

    run_validate
    if [ $RUN_VALIDATE_EXIT_CODE -eq 0 ]; then
        pass "validate_puid_pgid accepts valid positive integers"
    else
        fail "validate_puid_pgid should accept valid positive integers"
    fi
}

test_validate_puid_pgid_zero() {
    echo "Testing validate_puid_pgid with zero values..."

    export PUID=0
    export PGID=0
    run_validate

    if [ $RUN_VALIDATE_EXIT_CODE -ne 0 ] && echo "$RUN_VALIDATE_OUTPUT" | grep -q "Error: PUID=0"; then
        pass "validate_puid_pgid rejects PUID=0"
    else
        fail "validate_puid_pgid should reject PUID=0 (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi

    export PUID=1000
    export PGID=0
    run_validate

    if [ $RUN_VALIDATE_EXIT_CODE -ne 0 ] && echo "$RUN_VALIDATE_OUTPUT" | grep -q "Error: PGID=0"; then
        pass "validate_puid_pgid rejects PGID=0"
    else
        fail "validate_puid_pgid should reject PGID=0 (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi
}

test_validate_puid_pgid_invalid() {
    echo "Testing validate_puid_pgid with non-numeric values..."

    export PUID="abc"
    export PGID=1000
    run_validate

    if [ $RUN_VALIDATE_EXIT_CODE -ne 0 ] && echo "$RUN_VALIDATE_OUTPUT" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects non-numeric PUID"
    else
        fail "validate_puid_pgid should reject non-numeric PUID (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi

    export PUID=1000
    export PGID="xyz"
    run_validate

    if [ $RUN_VALIDATE_EXIT_CODE -ne 0 ] && echo "$RUN_VALIDATE_OUTPUT" | grep -q "Error: PGID must be a positive integer"; then
        pass "validate_puid_pgid rejects non-numeric PGID"
    else
        fail "validate_puid_pgid should reject non-numeric PGID (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi
}

test_script_defaults_empty_puid_pgid() {
    echo "Testing script default substitution for empty PUID/PGID..."

    export PUID=""
    export PGID=1000
    run_validate '&& echo "$PUID"'

    if [ $RUN_VALIDATE_EXIT_CODE -eq 0 ] && [ "$RUN_VALIDATE_OUTPUT" = "1000" ]; then
        pass "script defaults empty PUID to 1000"
    else
        fail "script should default empty PUID to 1000 (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi

    export PUID=1000
    export PGID=""
    run_validate '&& echo "$PGID"'

    if [ $RUN_VALIDATE_EXIT_CODE -eq 0 ] && [ "$RUN_VALIDATE_OUTPUT" = "1000" ]; then
        pass "script defaults empty PGID to 1000"
    else
        fail "script should default empty PGID to 1000 (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi
}

test_validate_puid_pgid_negative() {
    echo "Testing validate_puid_pgid with negative values..."

    export PUID=-1
    export PGID=1000
    run_validate

    if [ $RUN_VALIDATE_EXIT_CODE -ne 0 ] && echo "$RUN_VALIDATE_OUTPUT" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects negative PUID"
    else
        fail "validate_puid_pgid should reject negative PUID (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi
}

test_validate_puid_pgid_mixed_chars() {
    echo "Testing validate_puid_pgid with mixed alphanumeric values..."

    export PUID="1000abc"
    export PGID=1000
    run_validate

    if [ $RUN_VALIDATE_EXIT_CODE -ne 0 ] && echo "$RUN_VALIDATE_OUTPUT" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects mixed alphanumeric PUID"
    else
        fail "validate_puid_pgid should reject mixed alphanumeric PUID (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi
}

test_script_defaults_unset_puid_pgid() {
    echo "Testing script default substitution for unset PUID/PGID..."

    unset PUID
    export PGID=1000
    run_validate '&& echo "$PUID"'

    if [ $RUN_VALIDATE_EXIT_CODE -eq 0 ] && [ "$RUN_VALIDATE_OUTPUT" = "1000" ]; then
        pass "script defaults unset PUID to 1000"
    else
        fail "script should default unset PUID to 1000 (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi

    export PUID=1000
    unset PGID
    run_validate '&& echo "$PGID"'

    if [ $RUN_VALIDATE_EXIT_CODE -eq 0 ] && [ "$RUN_VALIDATE_OUTPUT" = "1000" ]; then
        pass "script defaults unset PGID to 1000"
    else
        fail "script should default unset PGID to 1000 (exit_code=$RUN_VALIDATE_EXIT_CODE, result=$RUN_VALIDATE_OUTPUT)"
    fi
}

echo "================================"
echo "Entrypoint.sh Test Suite"
echo "================================"
echo ""

test_validate_puid_pgid_valid
test_validate_puid_pgid_zero
test_validate_puid_pgid_invalid
test_script_defaults_empty_puid_pgid
test_validate_puid_pgid_negative
test_validate_puid_pgid_mixed_chars
test_script_defaults_unset_puid_pgid

echo ""
echo "================================"
echo "Test Results"
echo "================================"
echo "Total: $TOTAL"
echo "Passed: $PASS"
echo "Failed: $FAIL"
echo ""

if [ $FAIL -gt 0 ]; then
    echo "Some tests failed!"
    exit 1
else
    echo "All tests passed!"
    exit 0
fi
