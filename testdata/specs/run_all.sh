#!/bin/bash
# Run the compschema round-trip pipeline against multiple OpenAPI specs.
# Usage: ./testdata/specs/run_all.sh

set -e
cd "$(dirname "$0")/../.."

COMPSCHEMA_BIN="$(go env GOPATH)/bin/compschema"
if [ ! -f "$COMPSCHEMA_BIN" ]; then
    echo "Installing compschema..."
    go install ./cmd/compschema/
fi
COMPSCHEMA="$COMPSCHEMA_BIN"
RESULTS=""
PASS=0
FAIL=0
TOTAL=0

for spec in testdata/specs/*.yaml testdata/specs/*.json; do
    [ -f "$spec" ] || continue
    name=$(basename "$spec" | sed 's/\.\(yaml\|json\)$//')
    
    # Skip sources.txt
    [ "$name" = "sources" ] && continue
    
    TOTAL=$((TOTAL + 1))
    dir="/tmp/compschema-specs/${name}"
    rm -rf "$dir"
    mkdir -p "$dir"
    
    echo "━━━ ${name} ━━━"
    
    # Step 1: Extract all component schemas
    if ! $COMPSCHEMA extract --spec "$spec" --path / --validate --out "${dir}/schema.json" 2>&1; then
        # Try extracting just components (no path filter)
        # Some specs have no paths or different structure
        echo "  ⚠ path extraction failed, trying single schema extraction..."
        # Get first schema name
        first_schema=$($COMPSCHEMA schemas --spec "$spec" 2>/dev/null | grep '^\s' | head -1 | tr -d ' ' || true)
        if [ -z "$first_schema" ]; then
            echo "  ✗ no schemas found"
            FAIL=$((FAIL + 1))
            RESULTS="${RESULTS}\n  ✗ ${name}: no schemas found"
            continue
        fi
        if ! $COMPSCHEMA extract --spec "$spec" --schema "$first_schema" --validate --out "${dir}/schema.json" 2>&1; then
            echo "  ✗ extract failed"
            FAIL=$((FAIL + 1))
            RESULTS="${RESULTS}\n  ✗ ${name}: extract failed"
            continue
        fi
    fi
    
    schema_size=$(wc -c < "${dir}/schema.json" | tr -d ' ')
    defs_count=$(python3 -c "import json; d=json.load(open('${dir}/schema.json')); print(len(d.get('\$defs',{})))" 2>/dev/null || echo "?")
    
    # Step 2: Import into Go
    if ! $COMPSCHEMA import --package "${name}" --out "${dir}/types.go" "${dir}/schema.json" 2>&1; then
        echo "  ✗ import failed"
        FAIL=$((FAIL + 1))
        RESULTS="${RESULTS}\n  ✗ ${name}: import failed (${defs_count} defs)"
        continue
    fi
    
    types_count=$(grep -c '^type ' "${dir}/types.go" 2>/dev/null || echo "0")
    
    # Step 3: Check Go compiles
    printf "module ${name}\n\ngo 1.22\n" > "${dir}/go.mod"
    if ! (cd "$dir" && go build ./... 2>&1); then
        echo "  ✗ Go compilation failed"
        FAIL=$((FAIL + 1))
        RESULTS="${RESULTS}\n  ✗ ${name}: compile failed (${defs_count} defs, ${types_count} Go types)"
        continue
    fi
    
    # Step 4: Generate schema back (must run from within the module)
    if ! (cd "$dir" && $COMPSCHEMA generate --all --out . . 2>&1); then
        echo "  ✗ generate failed"
        FAIL=$((FAIL + 1))
        RESULTS="${RESULTS}\n  ✗ ${name}: generate failed"
        continue
    fi
    
    # Step 4b: Run generated tests (skip for very large packages)
    test_pass=0
    test_skip=0
    test_fail=0
    if [ "$types_count" -lt 1000 ] 2>/dev/null; then
        if (cd "$dir" && go get github.com/santhosh-tekuri/jsonschema/v6 2>/dev/null); then
            test_output=$(cd "$dir" && timeout 60 go test -v -count=1 ./... 2>&1 || true)
            test_pass=$(echo "$test_output" | grep -cF -- '--- PASS' 2>/dev/null || echo 0)
            test_skip=$(echo "$test_output" | grep -cF -- '--- SKIP' 2>/dev/null || echo 0)
            test_fail=$(echo "$test_output" | grep -cF -- '--- FAIL' 2>/dev/null || echo 0)
        fi
    else
        test_pass="-"
        test_skip="-"
        test_fail="skip(large)"
    fi
    
    # Step 5: IR diff
    diff_output=$($COMPSCHEMA diff --ir "${dir}/schema.json" "${dir}/schema.gen.json" 2>&1)
    match_rate=$(echo "$diff_output" | grep 'Field match rate' | grep -oP '[\d.]+%' || echo "?")
    matched=$(echo "$diff_output" | grep 'Matched:' | head -1 | grep -oP '\d+' || echo "0")
    differ=$(echo "$diff_output" | grep 'Differ:' | grep -oP '\d+' || echo "0")
    
    echo "  ✓ ${defs_count} defs → ${types_count} Go types → ${match_rate} field match → tests: ${test_pass}p/${test_skip}s/${test_fail}f"
    PASS=$((PASS + 1))
    RESULTS="${RESULTS}\n  ✓ ${name}: ${defs_count} defs, ${types_count} types, ${match_rate} match, tests ${test_pass}p/${test_skip}s/${test_fail}f"
    
    # Cleanup
    rm -rf "$dir"
done

echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║         Multi-Spec Round-Trip Results            ║"
echo "╠══════════════════════════════════════════════════╣"
echo "║  Specs tested:  ${TOTAL}                               ║"
echo "║  Passed:        ${PASS}                               ║"
echo "║  Failed:        ${FAIL}                               ║"
echo "╠══════════════════════════════════════════════════╣"
echo -e "$RESULTS"
echo "╚══════════════════════════════════════════════════╝"
