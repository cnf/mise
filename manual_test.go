package main

import (
	"bytes"
	"strings"
	"testing"
)

func tick() []byte { return []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4} }

// p takes the address of a fixture id without addressing a map index directly.
func p(v int64) *int64 { return &v }

// TestManualFollowsSubAssembly checks a step that references a child assembly
// pulls that child's own steps in, and that its images come along.
func TestManualFollowsSubAssembly(t *testing.T) {
	db, ids := mkFixture(t)
	// a note on the carrier, which Frame's manual will follow
	noteID, err := addStep(db, &Step{AssemblyID: ids["carrier"], ItemType: "note", Title: "bolt it down", Body: "4x M4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addImage(db, noteID, "a.png", "image/png", tick(), "torqued"); err != nil {
		t.Fatal(err)
	}
	// Frame follows the carrier
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "note", Title: "unpack"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "assembly", ChildAssemblyID: p(ids["carrier"]), Title: "build carrier"}); err != nil {
		t.Fatal(err)
	}

	m, err := manual(db, ids["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(m.Steps))
	}
	ref := m.Steps[1].Reference
	if ref == nil {
		t.Fatal("step 2 should carry a resolved sub-manual")
	}
	if ref.Title != "Carrier" {
		t.Errorf("sub-manual title = %q, want Carrier", ref.Title)
	}
	if len(ref.Steps) != 1 || ref.Steps[0].Title != "bolt it down" {
		t.Fatalf("sub-manual steps wrong: %+v", ref.Steps)
	}
	if len(ref.Steps[0].Images) != 1 || ref.Steps[0].Images[0].Caption != "torqued" {
		t.Errorf("sub-manual step lost its image: %+v", ref.Steps[0].Images)
	}
}

// TestManualSlotFollowsChosenOption is the phase-2/3 seam: a slot step in a
// manual must follow whichever option the build selected, so the same manual
// reads differently for rev A and rev B.
func TestManualSlotFollowsChosenOption(t *testing.T) {
	db, ids := mkFixture(t)
	// give each PCB revision its own manual step
	if _, err := addStep(db, &Step{AssemblyID: ids["pcbA"], ItemType: "note", Title: "rev A: solder the SMD header"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["pcbB"], ItemType: "note", Title: "rev B: fit the standoffs first"}); err != nil {
		t.Fatal(err)
	}
	// Carrier's manual just defers to the PCB slot
	if _, err := addStep(db, &Step{AssemblyID: ids["carrier"], ItemType: "slot", SlotID: p(ids["slot"]), Title: "the board"}); err != nil {
		t.Fatal(err)
	}

	// default (rev A)
	m, err := manual(db, ids["carrier"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Steps[0].ChosenLabel; got != "rev A" {
		t.Errorf("default slot label = %q, want rev A", got)
	}
	if m.Steps[0].Reference == nil || m.Steps[0].Reference.Steps[0].Title != "rev A: solder the SMD header" {
		t.Fatalf("default should follow PCB rev A: %+v", m.Steps[0].Reference)
	}

	// explicit rev B
	sel := Selection{}
	if err := sel.Set(db, ids["slot"], ids["optB"]); err != nil {
		t.Fatal(err)
	}
	m2, err := manual(db, ids["carrier"], sel)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.Steps[0].ChosenLabel; got != "rev B" {
		t.Errorf("explicit slot label = %q, want rev B", got)
	}
	if m2.Steps[0].Reference == nil || m2.Steps[0].Reference.Steps[0].Title != "rev B: fit the standoffs first" {
		t.Fatalf("rev B build should follow PCB rev B: %+v", m2.Steps[0].Reference)
	}
}

// TestManualLoopGuard checks a reference that points back up the tree is marked
// and not followed, rather than recursing forever.
func TestManualLoopGuard(t *testing.T) {
	db, ids := mkFixture(t)
	// carrier's manual follows frame; frame's manual follows carrier.
	if _, err := addStep(db, &Step{AssemblyID: ids["carrier"], ItemType: "assembly", ChildAssemblyID: p(ids["frame"]), Title: "wrap up"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "assembly", ChildAssemblyID: p(ids["carrier"]), Title: "build carrier"}); err != nil {
		t.Fatal(err)
	}
	m, err := manual(db, ids["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := m.Steps[0].Reference
	if inner == nil || len(inner.Steps) == 0 {
		t.Fatal("expected the carrier sub-manual")
	}
	back := inner.Steps[0].Reference
	if back == nil || !back.Loop {
		t.Fatalf("the reference back to Frame should be marked Loop, got %+v", back)
	}
	if len(back.Steps) != 0 {
		t.Errorf("a loop reference must not be followed, got %d steps", len(back.Steps))
	}
}

// TestStepReorder checks the up/down swap, including the no-op at the ends.
func TestStepReorder(t *testing.T) {
	db, ids := mkFixture(t)
	a, _ := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "note", Title: "first"})
	b, _ := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "note", Title: "second"})

	if err := moveStep(db, b, -1); err != nil {
		t.Fatal(err)
	}
	got, _ := steps(db, ids["frame"])
	if got[0].Title != "second" || got[1].Title != "first" {
		t.Fatalf("after moving second up: %q, %q", got[0].Title, got[1].Title)
	}
	// already first: moving up again is a no-op, not an error
	if err := moveStep(db, got[0].ID, -1); err != nil {
		t.Fatalf("moving the first step up should be a no-op, got %v", err)
	}
	// moving the last step down is a no-op too
	if err := moveStep(db, got[1].ID, 1); err != nil {
		t.Fatalf("moving the last step down should be a no-op, got %v", err)
	}
	_ = a
}

// TestImageValidation checks the size and type guards, and that the bytes come
// back out byte-for-byte.
func TestImageValidation(t *testing.T) {
	db, ids := mkFixture(t)
	stepID, _ := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "note", Title: "x"})

	if _, err := addImage(db, stepID, "x.svg", "image/svg+xml", tick(), ""); err == nil {
		t.Error("svg should be rejected (it can carry script)")
	}
	if _, err := addImage(db, stepID, "e.png", "image/png", nil, ""); err == nil {
		t.Error("empty file should be rejected")
	}
	big := make([]byte, maxImageBytes+1)
	if _, err := addImage(db, stepID, "big.png", "image/png", big, ""); err == nil {
		t.Error("oversized image should be rejected")
	}

	id, err := addImage(db, stepID, "ok.png", "image/png", tick(), "note")
	if err != nil {
		t.Fatal(err)
	}
	got, mime, err := imageBytes(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(tick()) || mime != "image/png" {
		t.Errorf("bytes round-trip failed: %v %q", got, mime)
	}
}

// TestDeleteGuardsForManual checks a slot or assembly in use by a manual step
// cannot be deleted out from under it with a raw foreign-key error.
func TestDeleteGuardsForManual(t *testing.T) {
	db, ids := mkFixture(t)
	if _, err := addStep(db, &Step{AssemblyID: ids["carrier"], ItemType: "slot", SlotID: p(ids["slot"])}); err != nil {
		t.Fatal(err)
	}
	if err := deleteSlot(db, ids["slot"]); err == nil {
		t.Error("deleting a slot referenced by a manual step should be refused")
	}
	if err := deleteAssembly(db, ids["pcbA"]); err == nil {
		t.Error("deleting an assembly that is a variant option target should be refused")
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "assembly", ChildAssemblyID: p(ids["carrier"])}); err != nil {
		t.Fatal(err)
	}
	if err := deleteAssembly(db, ids["carrier"]); err == nil {
		t.Error("deleting an assembly referenced by a manual step should be refused")
	}
}

// TestDocumentStep covers the phase-4 embedded board: a document step stores an
// iBOM, replaces it on re-upload, round-trips bytes, and refuses a file that is
// not an InteractiveHtmlBom export.
func TestDocumentStep(t *testing.T) {
	db, ids := mkFixture(t)
	stepID, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "document", Title: "the board"})
	if err != nil {
		t.Fatal(err)
	}
	// a note step does not get a document attached by stepDocument
	if d, err := stepDocument(db, stepID); err != nil || d != nil {
		t.Fatalf("a fresh document step should have no document: %v %v", d, err)
	}

	// refuse a file that is not an iBOM
	if _, err := addDocument(db, stepID, "not.html", []byte("<html>hi</html>")); err == nil {
		t.Error("a non-iBOM file should be refused")
	}
	if _, err := addDocument(db, stepID, "empty.html", nil); err == nil {
		t.Error("an empty file should be refused")
	}

	fake := []byte(`<html><script>var pcbdata = JSON.parse(LZString.decompressFromBase64("AAA"));</script></html>`)
	id, err := addDocument(db, stepID, "board.html", fake)
	if err != nil {
		t.Fatal(err)
	}
	got, mime, err := documentBytes(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(fake) {
		t.Errorf("document bytes did not round-trip")
	}
	if mime != "text/html; charset=utf-8" {
		t.Errorf("iBOM mime = %q, want text/html", mime)
	}
	if d, _ := stepDocument(db, stepID); d == nil || d.Filename != "board.html" || d.Size != int64(len(fake)) {
		t.Fatalf("stepDocument metadata wrong: %+v", d)
	}

	// re-upload replaces, so a re-exported board lands in place. SQLite reuses
	// the rowid after a delete (no AUTOINCREMENT), so assert on the content and
	// the count, not on the id changing.
	fake2 := []byte(`<html><script>var pcbdata = JSON.parse(LZString.decompressFromBase64("BBB"));</script></html>`)
	id2, err := addDocument(db, stepID, "board2.html", fake2)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM document WHERE step_id=?`, stepID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("re-upload should leave one document, got %d", count)
	}
	if d, _ := stepDocument(db, stepID); d == nil || d.ID != id2 || d.Filename != "board2.html" {
		t.Errorf("step should now point at the replacement, got %+v", d)
	}
	got2, _, err := documentBytes(db, id2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != string(fake2) {
		t.Error("the replacement document's bytes are wrong")
	}
	// deleting the step cascades to its document
	if err := deleteStep(db, stepID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := documentBytes(db, id2); err == nil {
		t.Error("deleting the step should cascade to its document")
	}
}

// TestManualTemplatesRender covers the pages added in phase 3, in the same test
// that guards pointer-dereference mistakes: every page must execute.
func TestManualTemplatesRender(t *testing.T) {
	tmpl := parseTemplates(t)
	db, ids := mkFixture(t)
	noteID, _ := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "note", Title: "hello", Body: "line1\nline2"})
	if _, err := addImage(db, noteID, "p.png", "image/png", tick(), "cap"); err != nil {
		t.Fatal(err)
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "assembly", ChildAssemblyID: p(ids["carrier"]), Title: "carrier"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "slot", SlotID: p(ids["slot"]), Title: "board"}); err != nil {
		t.Fatal(err)
	}
	docStep, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "document", Title: "the board"})
	if err != nil {
		t.Fatal(err)
	}
	fake := []byte(`<html><script>var pcbdata = JSON.parse(LZString.decompressFromBase64("AAA"));</script></html>`)
	if _, err := addDocument(db, docStep, "board.html", fake); err != nil {
		t.Fatal(err)
	}

	m, err := manual(db, ids["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := getAssembly(db, ids["frame"])
	asms, _ := listAssemblies(db)
	parts, _ := listParts(db, "")
	slots, _ := listSlots(db, ids["frame"])

	var buf bytes.Buffer
	data := map[string]any{
		"Assembly": a, "Manual": m, "Parts": parts, "All": asms,
		"Slots": slots, "Label": "build: test", "BuildQuery": "1", "Back": "/assemblies/1",
	}
	if err := tmpl.ExecuteTemplate(&buf, "manual_view.html", data); err != nil {
		t.Fatalf("manual_view: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Manual:", "hello", "cap", "variant:", "sub-manual", `src="/documents/`, "board.html"} {
		if !strings.Contains(out, want) {
			t.Errorf("manual page missing %q", want)
		}
	}
}
