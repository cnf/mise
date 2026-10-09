package web

import (
	"os"
	"strings"
	"testing"

	"example.com/cnf/mise/internal/bom"
	"example.com/cnf/mise/internal/ibom"
	"example.com/cnf/mise/internal/store"
)

// TestGatherRollup checks the flatten: quantity multiplication through
// sub-assemblies, grouping by declaring sub-assembly, DNP exclusion, and
// shared-part summation.
func TestGatherRollup(t *testing.T) {
	db, err := store.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mkPart := func(name, kind string) int64 {
		id, err := store.UpsertPart(db, &store.Part{Name: name, Kind: kind, Attrs: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mkAsm := func(name string) int64 {
		id, err := store.UpsertAssembly(db, &store.Assembly{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	bolt := mkPart("M4x20 button head", "fastener")
	nut := mkPart("T-slot nut", "extrusion")

	// child: 4 bolts
	child := mkAsm("Bracket")
	if _, err := store.AddLine(db, &store.Line{AssemblyID: child, ItemType: "part", PartID: &bolt, Qty: 4}); err != nil {
		t.Fatal(err)
	}

	// root: 2x child, 8 nuts, and 1 DNP bolt line
	root := mkAsm("Frame")
	if _, err := store.AddLine(db, &store.Line{AssemblyID: root, ItemType: "assembly", ChildAssemblyID: &child, Qty: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddLine(db, &store.Line{AssemblyID: root, ItemType: "part", PartID: &nut, Qty: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddLine(db, &store.Line{AssemblyID: root, ItemType: "part", PartID: &bolt, Qty: 99, DNP: true}); err != nil {
		t.Fatal(err)
	}

	g, err := bom.Gather(db, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", g.Errors)
	}
	// 2 brackets x 4 bolts = 8 bolts (DNP 99 excluded)
	got := map[string]float64{}
	for _, p := range g.Total {
		got[p.Name] = p.Qty
	}
	if got["M4x20 button head"] != 8 {
		t.Errorf("bolts = %v, want 8 (2x4, DNP excluded)", got["M4x20 button head"])
	}
	if got["T-slot nut"] != 8 {
		t.Errorf("nuts = %v, want 8", got["T-slot nut"])
	}
	// grouping: the bolt belongs to the Bracket group, the nut to the Frame group
	for _, grp := range g.Groups {
		for _, l := range grp.Lines {
			if grp.AssemblyName == "Bracket" && l.Name != "M4x20 button head" {
				t.Errorf("Bracket group should only hold bolts, got %q", l.Name)
			}
		}
	}
}

func TestCycleGuard(t *testing.T) {
	db, err := store.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, _ := store.UpsertAssembly(db, &store.Assembly{Name: "A"})
	b, _ := store.UpsertAssembly(db, &store.Assembly{Name: "B"})
	if err := store.WouldCycle(db, a, b); err != nil {
		t.Fatalf("A->B should be allowed: %v", err)
	}
	// make B a child of A, then A can no longer be added under B
	if _, err := store.AddLine(db, &store.Line{AssemblyID: a, ItemType: "assembly", ChildAssemblyID: &b, Qty: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.WouldCycle(db, b, a); err == nil {
		t.Fatal("B->A must be rejected as a cycle")
	}
	if err := store.WouldCycle(db, a, a); err == nil {
		t.Fatal("self-reference must be rejected")
	}
}

// TestIBOMDecode is a golden test against a real InteractiveHtmlBom file, kept
// out of the repo. It skips when the file is absent so the suite still runs.
func TestIBOMDecode(t *testing.T) {
	const f = "ibom.html"
	if _, err := os.Stat(f); err != nil {
		t.Skip("no ibom.html present")
	}
	db, err := store.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	asmID, n, err := ibom.ImportIBOM(db, raw, "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n == 0 {
		t.Fatal("imported zero components")
	}
	a, err := store.GetAssembly(db, asmID)
	if err != nil || a == nil {
		t.Fatalf("assembly not created: %v", err)
	}
	if !strings.Contains(a.Name, "FunShoot") {
		t.Errorf("assembly name %q should carry the board title", a.Name)
	}
	ls, err := store.Lines(db, asmID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) == 0 {
		t.Fatal("no lines imported")
	}
	// the ESP32 module is on the board; verify a part with a real value+footprint
	// came through as a component line.
	found := false
	for _, l := range ls {
		if strings.Contains(l.PartName, "ESP32-S3-WROOM-1") {
			found = true
		}
	}
	if !found {
		t.Error("expected an ESP32-S3-WROOM-1 line")
	}
	t.Logf("imported %d refdes across %d lines into %q", n, len(ls), a.Name)
}

// The iBOM decoder is exercised end-to-end by TestIBOMDecode against a real
// file. A synthetic "known payload" test would assert nothing: LZString's
// output depends on the encoder's internal state, so a hand-written string
// proves only that the decoder terminates, not that it is correct.
