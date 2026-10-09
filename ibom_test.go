package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

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
