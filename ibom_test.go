package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestImportKeepsTheBoardViewable is the fix for "there is no way to view the
// imported iBOM": importing used to decode the board into BOM lines and throw the
// file away, so the layout you imported for was unreachable. Now the file is kept
// and attached to the assembly's manual.
func TestImportKeepsTheBoardViewable(t *testing.T) {
	raw, err := os.ReadFile("ibom.html")
	if err != nil {
		t.Skip("no ibom.html present")
	}
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	asmID, n, err := importIBOM(db, raw, "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n == 0 {
		t.Fatal("imported zero components")
	}
	if err := attachImportedIBOM(db, asmID, raw); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// the board is now reachable from the assembly's manual
	ss, err := steps(db, asmID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ItemType != "document" {
		t.Fatalf("expected one document step, got %+v", ss)
	}
	if ss[0].Document == nil {
		t.Fatal("the document step has no board attached")
	}
	got, mime, err := documentBytes(db, ss[0].Document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "text/html; charset=utf-8" {
		t.Errorf("imported board mime = %q, want text/html", mime)
	}
	if len(got) != len(raw) {
		t.Errorf("stored board is %d bytes, original %d — not the same file", len(got), len(raw))
	}

	// re-importing must replace, not stack duplicate steps
	if _, _, err := importIBOM(db, raw, ""); err != nil {
		t.Fatal(err)
	}
	if err := attachImportedIBOM(db, asmID, raw); err != nil {
		t.Fatal(err)
	}
	ss2, _ := steps(db, asmID)
	if len(ss2) != 1 {
		t.Errorf("re-import stacked steps: got %d, want 1", len(ss2))
	}
	var docs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM document`).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Errorf("re-import left %d documents, want 1", docs)
	}
}

// TestDecodePcbdataFixture decodes a real LZString payload extracted from a
// KiCad InteractiveHtmlBom file (testdata/pcbdata.b64) and checks the parts of
// the structure the importer depends on. This is the decoder's regression test:
// it is committed with the repo, so it runs everywhere, unlike the full-file
// import which needs a generated ibom.html.
func TestDecodePcbdataFixture(t *testing.T) {
	js := loadFixtureJSON(t)
	var d ibomData
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Metadata.Title == "" {
		t.Error("metadata.title empty — expected the board title")
	}
	if len(d.Bom.Fields) == 0 {
		t.Error("bom.fields empty — expected per-line value/footprint")
	}
	if len(d.Bom.Both) == 0 {
		t.Error("bom.both empty — expected refdes groups")
	}
	if len(d.Footprints) == 0 {
		t.Error("footprints empty — expected on-board geometry")
	}
	t.Logf("decoded %d bytes: title=%q rev=%q fields=%d groups=%d skips=%d footprints=%d",
		len(js), d.Metadata.Title, d.Metadata.Revision,
		len(d.Bom.Fields), len(d.Bom.Both), len(d.Bom.Skipped), len(d.Footprints))
}

func loadFixtureJSON(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/pcbdata.b64")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	js, err := decompressFromBase64(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !json.Valid([]byte(js)) {
		t.Fatalf("decoded output is not valid JSON")
	}
	return js
}

// TestImportGroupsIdenticalParts is the fix for "the import adds every component
// once ... same part, 3 times in a row, just saying count: 1". The board's
// bom.both already groups components that share value+footprint (a group is a
// slice of [refdes, fieldIndex] pairs); the importer used to key by field index
// instead of group, which split one part across several lines.
//
// It runs off the committed blob fixture, so it works without the generated
// ibom.html: the importer is fed a board whose *only* resource is the pcbdata
// blob by wrapping the base64 payload back into the minimal shape ibomRe reads.
func TestImportGroupsIdenticalParts(t *testing.T) {
	js := loadFixtureJSON(t)
	var d ibomData
	if err := json.Unmarshal([]byte(js), &d); err != nil {
		t.Fatal(err)
	}

	// what the board says: one part per bom.both group, minus fully-skipped ones
	skipped := map[int]bool{}
	for _, s := range d.Bom.Skipped {
		skipped[s] = true
	}
	wantLines, wantComponents, wantDistinct := 0, 0, 0
	seen := map[string]bool{}
	for _, g := range d.Bom.Both {
		refs, allSkipped, fi := 0, true, -1
		for _, e := range g {
			if len(e) != 2 {
				continue
			}
			if _, ok := e[0].(string); !ok {
				continue
			}
			ffi, ok := e[1].(float64)
			if !ok {
				continue
			}
			refs++
			if fi < 0 {
				fi = int(ffi)
			}
			if !skipped[int(ffi)] {
				allSkipped = false
			}
		}
		if refs == 0 || allSkipped || (fi >= 0 && skipped[fi]) {
			continue
		}
		wantLines++
		wantComponents += refs
		key, _ := json.Marshal(d.Bom.Fields[fmt.Sprintf("%d", fi)])
		seen[string(key)] = true
	}
	wantDistinct = len(seen)

	// replay the importer over the fixture by rebuilding a minimal ibom file
	gotLines, gotComponents, gotDistinct, dup := importFixture(t)
	if gotLines != wantLines {
		t.Errorf("imported %d lines, board has %d groups — same part is split or collapsed", gotLines, wantLines)
	}
	if gotComponents != wantComponents {
		t.Errorf("imported %d components, board has %d placements", gotComponents, wantComponents)
	}
	if gotDistinct != wantDistinct {
		t.Errorf("imported %d distinct parts, board has %d", gotDistinct, wantDistinct)
	}
	if dup != 0 {
		t.Errorf("%d part names appear on more than one line — identical parts are not grouped", dup)
	}
	t.Logf("grouping: %d lines, %d components, %d distinct parts, %d duplicate names",
		gotLines, gotComponents, gotDistinct, dup)
}

// importFixture round-trips the fixture blob through a minimal ibom.html so the
// real importIBOM is exercised, and reports the resulting shape.
func importFixture(t *testing.T) (lines, components, distinct, dupNames int) {
	t.Helper()
	// re-encode the payload with the same LZString scheme the importer decodes.
	// We can avoid re-implementing compress by using the original base64 text.
	b64, err := os.ReadFile("testdata/pcbdata.b64")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	fake := "pcbdata = JSON.parse(LZString.decompressFromBase64(\"" +
		strings.TrimSpace(string(b64)) + "\"));"
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	asmID, n, err := importIBOM(db, []byte(fake), "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	components = n
	rows, err := db.Query(`SELECT l.qty, p.name FROM assembly_line l
		JOIN part p ON p.id=l.part_id WHERE l.assembly_id=?`, asmID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var qty int
		var name string
		if err := rows.Scan(&qty, &name); err != nil {
			t.Fatal(err)
		}
		lines++
		counts[name]++
	}
	distinct = len(counts)
	for _, c := range counts {
		if c > 1 {
			dupNames++
		}
	}
	return
}
