#!/bin/sh

PASS=0
FAIL=0
TOTAL=0

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

test_validate_puid_pgid_valid() {
    echo "Testing validate_puid_pgid with valid values..."
    
    export PUID=1000
    export PGID=1000
    
    if sh -c '. ./entrypoint.sh && validate_puid_pgid'; then
        pass "validate_puid_pgid accepts valid positive integers"
    else
        fail "validate_puid_pgid should accept valid positive integers"
    fi
}

test_validate_puid_pgid_zero() {
    echo "Testing validate_puid_pgid with zero values..."
    
    export PUID=0
    export PGID=0
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid' 2>&1)
    exit_code=$?
    
    if [ $exit_code -ne 0 ] && echo "$result" | grep -q "Error: PUID=0"; then
        pass "validate_puid_pgid rejects PUID=0"
    else
        fail "validate_puid_pgid should reject PUID=0 (exit_code=$exit_code, result=$result)"
    fi
    
    export PUID=1000
    export PGID=0
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid' 2>&1)
    exit_code=$?
    
    if [ $exit_code -ne 0 ] && echo "$result" | grep -q "Error: PGID=0"; then
        pass "validate_puid_pgid rejects PGID=0"
    else
        fail "validate_puid_pgid should reject PGID=0 (exit_code=$exit_code, result=$result)"
    fi
}

test_validate_puid_pgid_invalid() {
    echo "Testing validate_puid_pgid with invalid values..."
    
    export PUID="abc"
    export PGID=1000
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid' 2>&1)
    exit_code=$?
    
    if [ $exit_code -ne 0 ] && echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects non-numeric PUID"
    else
        fail "validate_puid_pgid should reject non-numeric PUID (exit_code=$exit_code, result=$result)"
    fi
    
    export PUID=1000
    export PGID="xyz"
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid' 2>&1)
    exit_code=$?
    
    if [ $exit_code -ne 0 ] && echo "$result" | grep -q "Error: PGID must be a positive integer"; then
        pass "validate_puid_pgid rejects non-numeric PGID"
    else
        fail "validate_puid_pgid should reject non-numeric PGID (exit_code=$exit_code, result=$result)"
    fi
    
    export PUID=""
    export PGID=1000
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid && echo "$PUID"' 2>&1)
    exit_code=$?
    
    if [ $exit_code -eq 0 ] && [ "$result" = "1000" ]; then
        pass "validate_puid_pgid accepts empty PUID and applies default 1000"
    else
        fail "validate_puid_pgid should accept empty PUID and set default to 1000 (exit_code=$exit_code, result=$result)"
    fi
    
    export PUID=1000
    export PGID=""
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid && echo "$PGID"' 2>&1)
    exit_code=$?
    
    if [ $exit_code -eq 0 ] && [ "$result" = "1000" ]; then
        pass "validate_puid_pgid accepts empty PGID and applies default 1000"
    else
        fail "validate_puid_pgid should accept empty PGID and set default to 1000 (exit_code=$exit_code, result=$result)"
    fi
}

test_validate_puid_pgid_negative() {
    echo "Testing validate_puid_pgid with negative values..."
    
    export PUID=-1
    export PGID=1000
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid' 2>&1)
    exit_code=$?
    
    if [ $exit_code -ne 0 ] && echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects negative PUID"
    else
        fail "validate_puid_pgid should reject negative PUID (exit_code=$exit_code, result=$result)"
    fi
}

test_validate_puid_pgid_mixed_chars() {
    echo "Testing validate_puid_pgid with mixed alphanumeric values..."
    
    export PUID="1000abc"
    export PGID=1000
    result=$(sh -c '. ./entrypoint.sh && validate_puid_pgid' 2>&1)
    exit_code=$?
    
    if [ $exit_code -ne 0 ] && echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects mixed alphanumeric PUID"
    else
        fail "validate_puid_pgid should reject mixed alphanumeric PUID (exit_code=$exit_code, result=$result)"
    fi
}

echo "================================"
echo "Entrypoint.sh Test Suite"
echo "================================"
echo ""

test_validate_puid_pgid_valid
test_validate_puid_pgid_zero
test_validate_puid_pgid_invalid
test_validate_puid_pgid_negative
test_validate_puid_pgid_mixed_chars

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
