package parser_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// ----------------------------------------------------------------------------
// Assertion guards
//
// Comment out any constant below to skip that field check across ALL fixtures.
// This is useful when a field's parsing is not yet validated or fully
// implemented — disable the check rather than deleting the fixture entry.
// ----------------------------------------------------------------------------

const (
	checkName        = true
	checkTypeName    = true
	checkDataType    = true
	checkIndexOffset = true
	checkSize        = true
)

// TestFixtures is the primary table-driven test runner.
//
// It globs testdata/*.yml, loads each paired .bin via LoadWithVerify (which
// includes a SHA-256 hash check), parses the binary with the Kaitai parser,
// and asserts every Symbols entry against the parsed output.
//
// To add a test case:
//  1. Run cmd/capture while the TwinCAT PLC is publishing Symbols.
//  2. Edit the generated .yml to set twincat_project_url and verify entries.
//  3. Re-run go test to confirm.
func TestFixtures(t *testing.T) {
	pattern := filepath.Join("..", "..", "testdata", "fixtures", "*", "captures", "*", "symbols", "message-*.yml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Skip("no fixtures found — run cmd/capture or cmd/runner to generate one")
	}

	for _, ymlPath := range matches {
		ymlPath := ymlPath
		// Build a readable test name: FixtureName/capture-stamp/message-ts
		fixtureName := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(ymlPath)))))
		captureName := filepath.Base(filepath.Dir(filepath.Dir(ymlPath)))
		msgName := strings.TrimSuffix(filepath.Base(ymlPath), ".yml")
		testName := fixtureName + "/" + captureName + "/" + msgName
		t.Run(testName, func(t *testing.T) {
			t.Parallel()
			runFixture(t, ymlPath)
		})
	}
}

func runFixture(t *testing.T, ymlPath string) {
	t.Helper()

	f, binData, err := fixture.LoadWithVerify(ymlPath)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	if f.ParseError != "" {
		t.Skipf("fixture records a parse error (fix the parser then re-run cmd/capture): %s",
			f.ParseError)
	}

	parsed, err := parseSymbolsBinary(binData)
	if err != nil {
		t.Fatalf("Kaitai parse failed: %v", err)
	}

	// Index parsed symbols by name for O(1) lookup.
	byName := make(map[string]parser.SymbolBody, len(parsed))
	for _, sym := range parsed {
		byName[sym.Name] = sym
	}

	if len(f.Entries) == 0 {
		t.Skip("fixture has no symbol entries — edit the .yml to add expected values")
	}

	for _, expected := range f.Entries {
		expected := expected
		t.Run(expected.Name, func(t *testing.T) {
			t.Parallel()
			assertSymbol(t, ymlPath, expected, byName)
		})
	}

	t.Logf("checked %d symbols from %s", len(f.Entries), filepath.Base(ymlPath))
}

func assertSymbol(
	t *testing.T,
	ymlPath string,
	expected fixture.SymbolEntry,
	byName map[string]parser.SymbolBody,
) {
	t.Helper()

	actual, ok := byName[expected.Name]
	if !ok {
		t.Errorf("symbol %q not found in parsed binary\n"+
			"  fixture: %s\n"+
			"  hint:    delete or rename this entry if the symbol no longer exists",
			expected.Name, ymlPath)
		return
	}

	if checkName && actual.Name != expected.Name {
		t.Errorf("name mismatch\n"+
			"  fixture:  %q\n"+
			"  parsed:   %q",
			expected.Name, actual.Name)
	}
	if checkTypeName && actual.TypeName != expected.TypeName {
		t.Errorf("type_name mismatch for %q\n"+
			"  fixture:  %q\n"+
			"  parsed:   %q",
			expected.Name, expected.TypeName, actual.TypeName)
	}
	if checkDataType && int(actual.DataType) != expected.DataType {
		t.Errorf("data_type mismatch for %q\n"+
			"  fixture:  %d\n"+
			"  parsed:   %d",
			expected.Name, expected.DataType, int(actual.DataType))
	}
	if checkIndexOffset && actual.IndexOffset != expected.IndexOffset {
		t.Errorf("index_offset mismatch for %q\n"+
			"  fixture:  %d (0x%08X)\n"+
			"  parsed:   %d (0x%08X)",
			expected.Name,
			expected.IndexOffset, expected.IndexOffset,
			actual.IndexOffset, actual.IndexOffset)
	}
	if checkSize && actual.Size != expected.Size {
		t.Errorf("size mismatch for %q\n"+
			"  fixture:  %d bytes\n"+
			"  parsed:   %d bytes",
			expected.Name, expected.Size, actual.Size)
	}
}

// parseSymbolsBinary runs the hand-written parser on data and returns all
// SymbolBody entries from the parsed stream.
func parseSymbolsBinary(data []byte) ([]parser.SymbolBody, error) {
	s, err := parser.ParseSymbolStream(data)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return s.Symbols, nil
}
