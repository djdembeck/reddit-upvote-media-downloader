#!/bin/sh
# Test suite for entrypoint.sh
# Run with: ./entrypoint.test.sh

set -e

PASS=0
FAIL=0
TOTAL=0

# Test helper functions
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

# Test validate_puid_pgid function
test_validate_puid_pgid_valid() {
    echo "Testing validate_puid_pgid with valid values..."
    
    # Test valid positive integers
    export PUID=1000
    export PGID=1000
    
    # Source the validation function (main-guard prevents execution)
    . ./entrypoint.sh
    
    # If we reach here, validation passed
    pass "validate_puid_pgid accepts valid positive integers"
}

test_validate_puid_pgid_zero() {
    echo "Testing validate_puid_pgid with zero values..."
    
    # Source functions first (main-guard prevents execution)
    . ./entrypoint.sh
    
    # Test PUID=0
    export PUID=0
    export PGID=0
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PUID=0"; then
        pass "validate_puid_pgid rejects PUID=0"
    else
        fail "validate_puid_pgid should reject PUID=0"
    fi
    
    # Test PGID=0
    export PUID=1000
    export PGID=0
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PGID=0"; then
        pass "validate_puid_pgid rejects PGID=0"
    else
        fail "validate_puid_pgid should reject PGID=0"
    fi
}

test_validate_puid_pgid_invalid() {
    echo "Testing validate_puid_pgid with invalid values..."
    
    # Source functions first
    . ./entrypoint.sh
    
    # Test non-numeric values
    export PUID="abc"
    export PGID=1000
    
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects non-numeric PUID"
    else
        fail "validate_puid_pgid should reject non-numeric PUID"
    fi
    
    export PUID=1000
    export PGID="xyz"
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PGID must be a positive integer"; then
        pass "validate_puid_pgid rejects non-numeric PGID"
    else
        fail "validate_puid_pgid should reject non-numeric PGID"
    fi
    
    # Test empty values
    export PUID=""
    export PGID=1000
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects empty PUID"
    else
        fail "validate_puid_pgid should reject empty PUID"
    fi
}

test_validate_puid_pgid_negative() {
    echo "Testing validate_puid_pgid with negative values..."
    
    # Source functions first
    . ./entrypoint.sh
    
    export PUID=-1
    export PGID=1000
    
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects negative PUID"
    else
        fail "validate_puid_pgid should reject negative PUID"
    fi
}

test_validate_puid_pgid_mixed_chars() {
    echo "Testing validate_puid_pgid with mixed alphanumeric values..."
    
    # Source functions first
    . ./entrypoint.sh
    
    export PUID="1000abc"
    export PGID=1000
    
    result=$(validate_puid_pgid 2>&1) || true
    
    if echo "$result" | grep -q "Error: PUID must be a positive integer"; then
        pass "validate_puid_pgid rejects mixed alphanumeric PUID"
    else
        fail "validate_puid_pgid should reject mixed alphanumeric PUID"
    fi
}

# Run all tests
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
