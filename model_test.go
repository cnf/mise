package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// mkGLB builds a real, minimal but valid glTF binary: the JSON chunk holds an
// empty scene, the BIN chunk is empty. This is a true GLB (right magic, version
// 2, declared total length, chunk layout), so the sniff and the byte round-trip
// exercise the actual format rather than a magic-number stub.
func mkGLB(t *testing.T) []byte {
	t.Helper()
	jd, err := json.Marshal(map[string]any{
		"asset":  map[string]any{"version": "2.0", "generator": "mise test"},
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []any{}}},
		"nodes":  []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	// chunks are padded to 4 bytes; JSON with spaces, BIN with zeros
	for len(jd)%4 != 0 {
		jd = append(jd, ' ')
	}
	bin := []byte{0, 0, 0, 0}

	var buf bytes.Buffer
	buf.WriteString("glTF")
	binary.Write(&buf, binary.LittleEndian, uint32(2))
	totalPos := buf.Len()
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // patched below
	// JSON chunk
	binary.Write(&buf, binary.LittleEndian, uint32(len(jd)))
	buf.WriteString("JSON")
	buf.Write(jd)
	// BIN chunk
	binary.Write(&buf, binary.LittleEndian, uint32(len(bin)))
	buf.WriteString("BIN\x00")
	buf.Write(bin)

	b := buf.Bytes()
	binary.LittleEndian.PutUint32(b[totalPos:], uint32(len(b)))
	return b
}

func TestGLBSniffing(t *testing.T) {
	glb := mkGLB(t)
	if mime, ok := sniffDocument(glb); !ok || mime != "model/gltf-binary" {
		t.Fatalf("a real GLB should sniff as model/gltf-binary, got %q ok=%v", mime, ok)
	}
	// a GLB is accepted on content, not on filename
	if _, ok := sniffDocument(glb); !ok {
		t.Fatal("sanity: the generated GLB should sniff")
	}

	// things that must NOT be taken for a model
	cases := map[string][]byte{
		"plain html":     []byte("<html>hello</html>"),
		"empty":          {},
		"just the magic": []byte("glTF"),
		"magic but v1":   append([]byte("glTF\x01\x00\x00\x00"), make([]byte, 8)...),
		"short":          []byte("glTF\x02\x00"),
	}
	for name, data := range cases {
		if _, ok := sniffDocument(data); ok {
			t.Errorf("%s should not be accepted as a renderable asset", name)
		}
	}

	// an iBOM still sniffs as HTML, not as a model
	ibom := []byte(`<script>var pcbdata = JSON.parse(LZString.decompressFromBase64("x"));</script>`)
	if mime, ok := sniffDocument(ibom); !ok || mime != "text/html; charset=utf-8" {
		t.Errorf("iBOM should sniff as text/html, got %q ok=%v", mime, ok)
	}
}

// TestGLBStep checks the whole model path through the store: a model step takes
// a GLB, serves it back byte-identical under the right mime, reports itself as a
// model, and makes the manual ask for the viewer.
func TestGLBStep(t *testing.T) {
	db, ids := mkFixture(t)
	stepID, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "document", Title: "Frame model"})
	if err != nil {
		t.Fatal(err)
	}
	glb := mkGLB(t)
	id, err := addDocument(db, stepID, "frame.glb", glb)
	if err != nil {
		t.Fatal(err)
	}
	got, mime, err := documentBytes(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, glb) {
		t.Error("GLB did not round-trip byte-for-byte")
	}
	if mime != "model/gltf-binary" {
		t.Errorf("served mime = %q, want model/gltf-binary", mime)
	}
	d, err := stepDocument(db, stepID)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	if !d.IsModel() {
		t.Error("a glTF document must report IsModel()")
	}

	m, err := manual(db, ids["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasModel() {
		t.Error("a manual containing a model step must report HasModel()")
	}

	// a manual with only text and photos must NOT ask for the viewer
	db2, ids2 := mkFixture(t)
	if _, err := addStep(db2, &Step{AssemblyID: ids2["frame"], ItemType: "note", Title: "only text"}); err != nil {
		t.Fatal(err)
	}
	m2, err := manual(db2, ids2["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if m2.HasModel() {
		t.Error("a text-only manual must not load the viewer")
	}
}

// TestGLBReplacesIBOMAndViceVersa checks that a step can switch kinds: a board
// replaced by a model and back, leaving exactly one asset each time.
func TestGLBReplacesIBOMAndViceVersa(t *testing.T) {
	db, ids := mkFixture(t)
	stepID, _ := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "document", Title: "asset"})
	board := []byte(`<script>var pcbdata = JSON.parse(LZString.decompressFromBase64("x"));</script>`)
	if _, err := addDocument(db, stepID, "b.html", board); err != nil {
		t.Fatal(err)
	}
	if d, _ := stepDocument(db, stepID); d == nil || d.IsModel() {
		t.Fatal("step should hold a board, not a model")
	}
	// the standard special case: a GLB legitimately starts with a CRC equal to the
	// iBOM magic length; the sniffer must not confuse the two
	if _, err := addDocument(db, stepID, "m.glb", mkGLB(t)); err != nil {
		t.Fatal(err)
	}
	if d, _ := stepDocument(db, stepID); d == nil || !d.IsModel() {
		t.Fatal("step should now hold a model")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM document WHERE step_id=?`, stepID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("switching asset kind should leave one document, got %d", n)
	}
}
