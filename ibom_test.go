package main

import (
	"encoding/json"
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
	raw, err := os.ReadFile("testdata/pcbdata.b64")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	js, err := decompressFromBase64(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if len(js) < 1000 {
		t.Fatalf("decoded only %d bytes — bit reader is wrong", len(js))
	}
	if !json.Valid([]byte(js)) {
		t.Fatalf("decoded output is not valid JSON (first 200 bytes: %q)", js[:200])
	}
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
