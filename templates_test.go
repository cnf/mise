package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// parseTemplates mirrors main(): the same FuncMap and the same file set. If a
// template action is malformed (Go templates reject operators like `*`, so a
// pointer dereference parses only at execution time in some shapes and not at
// all in others) this is where it must fail, not at process start.
func parseTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"trimNum": trimNum,
		"price":   func(p *float64) string { return strFmt(p) },
		"mul":     func(p *float64, q float64) string { return mulFmt(p, q) },
		"inc":     func(i int) int { return i + 1 },
	}).ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	return tmpl
}

// TestTemplatesParseAndRender is the guard that was missing: a broken template
// is a dead binary (every page 500s), and the store tests cannot catch it
// because they never touch templates. Each page gets a representative payload.
func TestTemplatesParseAndRender(t *testing.T) {
	tmpl := parseTemplates(t)

	db, ids := mkFixture(t)
	parts, err := listParts(db, "")
	if err != nil {
		t.Fatal(err)
	}
	asms, _ := listAssemblies(db)
	builds, _ := listBuilds(db)
	sel := Selection{}
	if err := sel.Set(db, ids["slot"], ids["optB"]); err != nil {
		t.Fatal(err)
	}
	g, err := gather(db, ids["frame"], sel)
	if err != nil {
		t.Fatal(err)
	}
	g.Selection = sel
	asmslots, err := slotsUnder(db, ids["frame"])
	if err != nil {
		t.Fatal(err)
	}
	type slotView struct {
		Slot     VariantSlot
		Options  []Option
		Selected int64
	}
	var views []slotView
	for _, sl := range asmslots {
		opts, _ := optionsForSlot(db, sl.ID)
		v := slotView{Slot: sl, Options: opts}
		if oid, ok := sel[sl.ID]; ok {
			v.Selected = oid
		}
		views = append(views, v)
	}
	frame, _ := getAssembly(db, ids["frame"])
	linesOf, _ := lines(db, ids["frame"])

	pages := map[string]any{
		"home.html":       map[string]any{"Parts": parts, "Assemblies": asms, "Builds": builds, "PartCount": len(parts), "AsmCount": len(asms), "BuildCount": len(builds)},
		"parts.html":      map[string]any{"Parts": parts, "Q": "", "Kinds": partKinds},
		"part_edit.html":  map[string]any{"Part": &Part{Currency: "EUR", Kind: "other", Attrs: "{}"}, "Kinds": partKinds},
		"part_view.html":  map[string]any{"Part": &Part{ID: 1, Name: "M4x20", Kind: "fastener", Attrs: "{}}"}, "Kinds": partKinds},
		"assemblies.html": map[string]any{"Rows": []any{}},
		"assembly_edit.html": map[string]any{
			"Assembly": frame, "Parts": parts, "All": asms,
			"Back": "/assemblies/1",
		},
		"assembly_view.html": map[string]any{
			"Assembly": frame, "Lines": linesOf, "Parts": parts, "All": asms,
			"Back": "/assemblies/1", "Edit": false,
		},
		"builds.html": map[string]any{"Builds": builds, "Assemblies": asms, "AsmNames": map[int64]string{}},
		"build_view.html": map[string]any{
			"Build":  &Build{ID: 1, Name: "Club rev B", AssemblyID: ids["frame"]},
			"Gather": g, "Assembly": frame, "Snapshot": nil, "Frozen": nil,
			"Sel": sel, "SlotViews": views, "Stale": false,
		},
		"gather.html":      map[string]any{"Gather": g},
		"ibom_import.html": map[string]any{},
	}

	for name, data := range pages {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("%s: execute: %v", name, err)
			continue
		}
		if buf.Len() < 20 {
			t.Errorf("%s: produced only %d bytes", name, buf.Len())
		}
	}

	// The build view must mark the chosen variant, and the gather view must list
	// the slot choice — those are the phase-2 behaviours worth asserting on the
	// rendered output rather than only on the model.
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "build_view.html", pages["build_view.html"]); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "rev B") {
		t.Error("build view does not show the selected option label")
	}
	if !strings.Contains(out, `value="`+itoa(ids["optB"])+`" selected`) {
		t.Errorf("build view does not mark option %d selected", ids["optB"])
	}

	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "gather.html", pages["gather.html"]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Variants used") {
		t.Error("gather view does not show the variants section")
	}
}

func itoa(i int64) string {
	return trimNum(float64(i))
}
