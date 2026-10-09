package main

import (
	"bytes"
	"strings"
	"testing"
)

// Read mode is the default; edit mode is opt-in (?edit=1). This is the rule the
// app had no way to express before: an assembly page was 30 form controls with
// nothing saying you were editing. These tests hold the line in both directions,
// because the failure mode is silent — a read page quietly full of controls.

// TestAssemblyReadModeHasNoEditControls: a GET (Edit=false) must render the
// assembly as something to look at, with no inputs that mutate anything.
func TestAssemblyReadModeHasNoEditControls(t *testing.T) {
	tmpl := parseTemplates(t)
	db, ids := mkFixture(t)
	a, _ := getAssembly(db, ids["frame"])
	ls, _ := lines(db, ids["frame"])
	parts, _ := listParts(db, "")
	asms, _ := listAssemblies(db)

	var buf bytes.Buffer
	data := map[string]any{
		"Assembly": a, "Lines": ls, "Parts": parts, "All": asms,
		"Back": "/assemblies/1", "Edit": false,
	}
	if err := tmpl.ExecuteTemplate(&buf, "assembly_view.html", data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// no mutation surfaces
	for _, banned := range []string{"_action", "<input", "<select", "<textarea"} {
		if strings.Contains(out, banned) {
			t.Errorf("read-mode assembly view contains %q — read mode must not be an editor", banned)
		}
	}
	// it still shows the content
	for _, want := range []string{"Contents", "M4x20", "Edit contents"} {
		if !strings.Contains(out, want) {
			t.Errorf("read-mode assembly view is missing %q", want)
		}
	}
}

// TestAssemblyEditModeHasEditControls: the opt-in editor must still be a full
// editor. A read-only fix that also broke editing would pass the test above.
func TestAssemblyEditModeHasEditControls(t *testing.T) {
	tmpl := parseTemplates(t)
	db, ids := mkFixture(t)
	a, _ := getAssembly(db, ids["frame"])
	ls, _ := lines(db, ids["frame"])
	parts, _ := listParts(db, "")
	asms, _ := listAssemblies(db)

	var buf bytes.Buffer
	data := map[string]any{
		"Assembly": a, "Lines": ls, "Parts": parts, "All": asms,
		"Back": "/assemblies/1", "Edit": true,
	}
	if err := tmpl.ExecuteTemplate(&buf, "assembly_view.html", data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"add_part", "add_sub", "add_slot", "editing", "_back"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit-mode assembly view is missing %q", want)
		}
	}
	// and edit controls must carry the mode back, so an add does not dump you
	// into read mode mid-task
	if !strings.Contains(out, "edit=1") {
		t.Error("edit-mode back-links do not preserve the edit mode")
	}
}

// TestManualReadModeHasNoEditControls is the same rule on the manual page, which
// is the denser of the two (recursive steps, each previously with 9 forms).
func TestManualReadModeHasNoEditControls(t *testing.T) {
	tmpl := parseTemplates(t)
	db, ids := mkFixture(t)
	noteID, _ := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "note", Title: "hello", Body: "text"})
	if _, err := addImage(db, noteID, "p.png", "image/png", tick(), "cap"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		edit bool
	}{{"read", false}, {"edit", true}} {
		m, err := manual(db, ids["frame"], nil)
		if err != nil {
			t.Fatal(err)
		}
		m.Edit = tc.edit
		m.Back = "/manuals/1"
		m.propagateMode(tc.edit, m.Back)
		a, _ := getAssembly(db, ids["frame"])
		asms, _ := listAssemblies(db)
		parts, _ := listParts(db, "")
		slots, _ := listSlots(db, ids["frame"])

		var buf bytes.Buffer
		data := map[string]any{
			"Assembly": a, "Manual": m, "Parts": parts, "All": asms,
			"Slots": slots, "Label": "", "BuildQuery": "", "Back": m.Back,
			"Edit": tc.edit, "NeedsViewer": m.HasModel(),
		}
		if err := tmpl.ExecuteTemplate(&buf, "manual_view.html", data); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		out := buf.String()

		if tc.edit {
			for _, want := range []string{"editing", "Add a step", "add image", "_action"} {
				if !strings.Contains(out, want) {
					t.Errorf("edit-mode manual missing %q", want)
				}
			}
			continue
		}
		// read mode: the step's text and its photo, but no editor
		for _, want := range []string{"hello", "cap", "Edit this manual"} {
			if !strings.Contains(out, want) {
				t.Errorf("read-mode manual missing %q", want)
			}
		}
		if strings.Contains(out, "_action") {
			t.Error("read-mode manual still contains mutation forms")
		}
	}
}

// TestPropagateModeReachesSubManuals: a sub-manual is rendered by the same
// recursive template, so if the mode is not pushed down a nested manual silently
// renders read-only while its parent is being edited.
func TestPropagateModeReachesSubManuals(t *testing.T) {
	db, ids := mkFixture(t)
	if _, err := addStep(db, &Step{AssemblyID: ids["carrier"], ItemType: "note", Title: "inner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addStep(db, &Step{AssemblyID: ids["frame"], ItemType: "assembly", ChildAssemblyID: p(ids["carrier"])}); err != nil {
		t.Fatal(err)
	}
	m, err := manual(db, ids["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Edit = true
	m.Back = "/manuals/1?edit=1"
	m.propagateMode(m.Edit, m.Back)

	ref := m.Steps[0].Reference
	if ref == nil {
		t.Fatal("expected a sub-manual reference")
	}
	if !ref.Edit {
		t.Error("sub-manual did not inherit edit mode")
	}
	if ref.Back != "/manuals/1?edit=1" {
		t.Errorf("sub-manual back-link = %q, want the parent's", ref.Back)
	}
}
