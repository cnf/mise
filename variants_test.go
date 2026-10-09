package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// mkFixture builds a small project: a Frame that contains a slot, where the
// slot's options are two different PCB revisions, one of which itself contains
// a sub-assembly. Quantity multiplication has to work through both a slot
// choice and the sub-assembly under it.
func mkFixture(t *testing.T) (*sql.DB, map[string]int64) {
	t.Helper()
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ids := map[string]int64{}

	mkPart := func(name string) int64 {
		id, err := upsertPart(db, &Part{Name: name, Kind: "electronic", Attrs: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mkAsm := func(name string) int64 {
		id, err := upsertAssembly(db, &Assembly{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	ids["carrier"] = mkAsm("Carrier")
	ids["pcbA"] = mkAsm("PCB rev A")
	ids["pcbB"] = mkAsm("PCB rev B")
	ids["frame"] = mkAsm("Frame")
	ids["bolt"] = mkPart("M4x20")
	ids["standoff"] = mkPart("Standoff")

	// PCB A is one board; PCB B is a board plus 2 standoffs (so choosing B pulls
	// extra parts in through the option's sub-assembly).
	standoff := ids["standoff"]
	if _, err := addLine(db, &Line{AssemblyID: ids["pcbB"], ItemType: "part", PartID: &standoff, Qty: 2}); err != nil {
		t.Fatal(err)
	}

	// Frame: 4 bolts + a slot over the two PCB revisions.
	bolt := ids["bolt"]
	if _, err := addLine(db, &Line{AssemblyID: ids["frame"], ItemType: "part", PartID: &bolt, Qty: 4}); err != nil {
		t.Fatal(err)
	}

	// The Carrier holds the slot, so the slot is not at the root — the picker
	// must find it through a sub-assembly edge.
	slot, err := addSlot(db, &VariantSlot{AssemblyID: ids["carrier"], Name: "PCB", Notes: "rev A/B share the frame"})
	if err != nil {
		t.Fatal(err)
	}
	ids["slot"] = slot
	if _, err := addLine(db, &Line{AssemblyID: ids["carrier"], ItemType: "slot", SlotID: &slot, Qty: 1}); err != nil {
		t.Fatal(err)
	}
	carrier := ids["carrier"]
	if _, err := addLine(db, &Line{AssemblyID: ids["frame"], ItemType: "assembly", ChildAssemblyID: &carrier, Qty: 1}); err != nil {
		t.Fatal(err)
	}

	// options: A (default), B
	ca := ids["pcbA"]
	cb := ids["pcbB"]
	optA, err := addOption(db, &Option{SlotID: slot, Label: "rev A", TargetType: "assembly", ChildAssemblyID: &ca, Qty: 1})
	if err != nil {
		t.Fatal(err)
	}
	optB, err := addOption(db, &Option{SlotID: slot, Label: "rev B", TargetType: "assembly", ChildAssemblyID: &cb, Qty: 1})
	if err != nil {
		t.Fatal(err)
	}
	ids["optA"] = optA
	ids["optB"] = optB
	return db, ids
}

func qtyOf(g *GatherResult, name string) float64 {
	for _, p := range g.Total {
		if p.Name == name {
			return p.Qty
		}
	}
	return -1
}

// TestFirstOptionIsDefault checks that adding the first option to a slot marks
// it default, so a build with no explicit choice still resolves.
func TestFirstOptionIsDefault(t *testing.T) {
	db, ids := mkFixture(t)
	opts, err := optionsForSlot(db, ids["slot"])
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 {
		t.Fatalf("want 2 options, got %d", len(opts))
	}
	if !opts[0].IsDefault || opts[1].IsDefault {
		t.Fatalf("exactly the first option should be default: %+v", opts)
	}
}

// TestSlotDefaultVsExplicit checks that a build with no selection resolves slot
// options to the default, and an explicit selection overrides it.
func TestSlotDefaultVsExplicit(t *testing.T) {
	db, ids := mkFixture(t)

	// no selection -> default (rev A): no standoffs
	g, err := gather(db, ids["frame"], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Errors) != 0 {
		t.Fatalf("errors: %v", g.Errors)
	}
	if got := qtyOf(g, "Standoff"); got != -1 {
		t.Errorf("default rev A should pull no standoffs, got qty %v", got)
	}
	if got := qtyOf(g, "M4x20"); got != 4 {
		t.Errorf("bolts = %v, want 4", got)
	}
	if len(g.Slots) != 1 || !g.Slots[0].IsDefault || g.Slots[0].Label != "rev A" {
		t.Fatalf("slot should resolve to default rev A: %+v", g.Slots)
	}

	// explicit rev B -> 2 standoffs appear
	sel := Selection{}
	if err := sel.Set(db, ids["slot"], ids["optB"]); err != nil {
		t.Fatal(err)
	}
	g2, err := gather(db, ids["frame"], sel)
	if err != nil {
		t.Fatal(err)
	}
	if got := qtyOf(g2, "Standoff"); got != 2 {
		t.Errorf("rev B should pull 2 standoffs, got %v", got)
	}
	if len(g2.Slots) != 1 || g2.Slots[0].IsDefault {
		t.Fatalf("slot should be an explicit choice: %+v", g2.Slots)
	}
	if g2.Slots[0].Label != "rev B" {
		t.Errorf("slot label = %q, want rev B", g2.Slots[0].Label)
	}
}

// TestSlotQuantityMultiplies checks that the option's own qty multiplies through
// the slot line and the enclosing sub-assembly.
func TestSlotQuantityMultiplies(t *testing.T) {
	db, ids := mkFixture(t)
	// bump the option to qty 3
	if _, err := db.Exec(`UPDATE variant_option SET qty=3 WHERE id=?`, ids["optB"]); err != nil {
		t.Fatal(err)
	}
	sel := Selection{}
	if err := sel.Set(db, ids["slot"], ids["optB"]); err != nil {
		t.Fatal(err)
	}
	g, err := gather(db, ids["frame"], sel)
	if err != nil {
		t.Fatal(err)
	}
	if got := qtyOf(g, "Standoff"); got != 6 {
		t.Errorf("3x rev B should pull 6 standoffs, got %v", got)
	}
}

// TestSelectionRejectsWrongSlot checks the guard that stops a tampered form
// pointing a slot at an unrelated option.
func TestSelectionRejectsWrongSlot(t *testing.T) {
	db, ids := mkFixture(t)
	other, err := addSlot(db, &VariantSlot{AssemblyID: ids["frame"], Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	sel := Selection{}
	if err := sel.Set(db, other, ids["optA"]); err == nil {
		t.Fatal("setting a slot to another slot's option must be rejected")
	}
}

// TestSlotsUnderFindsNestedSlot checks the picker discovers a slot reachable only
// through a sub-assembly edge, not just the root's own slots.
func TestSlotsUnderFindsNestedSlot(t *testing.T) {
	db, ids := mkFixture(t)
	slots, err := slotsUnder(db, ids["frame"])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range slots {
		if s.ID == ids["slot"] {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested slot not discovered; got %+v", slots)
	}
}

// TestCycleGuardThroughOption checks that cycle detection follows slot options,
// not only plain sub-assembly lines.
func TestCycleGuardThroughOption(t *testing.T) {
	db, ids := mkFixture(t)
	// Frame -> Carrier -> (slot -> PCB rev B). Making Frame an option of a slot
	// inside PCB rev B would close a loop; the guard must see it.
	slotInPCB, err := addSlot(db, &VariantSlot{AssemblyID: ids["pcbB"], Name: "Nested"})
	if err != nil {
		t.Fatal(err)
	}
	frame := ids["frame"]
	// direct check: can Frame be added under PCB rev B? No — B is under Frame.
	if err := wouldCycle(db, ids["pcbB"], frame); err == nil {
		t.Fatal("PCB rev B -> Frame must be rejected (B is under Frame)")
	}
	// and through the option edge specifically
	if _, err := db.Exec(`INSERT INTO variant_option (slot_id,label,target_type,child_assembly_id,qty)
		VALUES (?,?,'assembly',?,1)`, slotInPCB, "loop", frame); err != nil {
		t.Fatal(err)
	}
	if err := wouldCycle(db, frame, ids["pcbB"]); err == nil {
		t.Fatal("Frame -> PCB rev B must be rejected once B carries a slot option back to Frame")
	}
}

// TestMigrationFromPhase1 checks that a database created with the phase-1 schema
// is upgraded in place: rows survive the assembly_line rebuild, the widened
// CHECK accepts a slot line, and the migration is recorded (so a second open
// does not re-run it).
func TestMigrationFromPhase1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p1.db")

	// Build a phase-1 database by hand: old schema, no migrations table.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0].stmt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO assembly (id,name) VALUES (1,'Frame'),(2,'Bracket')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO part (id,name,kind) VALUES (5,'M4x20','fastener')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO assembly_line (id,assembly_id,item_type,part_id,qty) VALUES (1,1,'part',5,4)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO assembly_line (id,assembly_id,item_type,child_assembly_id,qty) VALUES (2,1,'assembly',2,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Opening through openDB must migrate it.
	db2, err := openDB(path)
	if err != nil {
		t.Fatalf("open+migrate: %v", err)
	}
	defer db2.Close()

	// rows preserved
	var n int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM assembly_line`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("after migration %d lines, want 2", n)
	}
	var qty float64
	if err := db2.QueryRow(`SELECT qty FROM assembly_line WHERE id=1`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 4 {
		t.Errorf("qty survived migration as %v, want 4", qty)
	}

	// the widened CHECK now accepts a slot line
	sl, err := addSlot(db2, &VariantSlot{AssemblyID: 1, Name: "PCB"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addLine(db2, &Line{AssemblyID: 1, ItemType: "slot", SlotID: &sl, Qty: 1}); err != nil {
		t.Fatalf("slot line rejected after migration: %v", err)
	}

	// migration recorded, so reopening is a no-op rather than a re-run
	if err := migrate(db2); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var applied int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != len(migrations) {
		t.Errorf("%d migrations recorded, want %d", applied, len(migrations))
	}

	// no dangling foreign keys
	var bad int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Errorf("%d foreign-key violations after migration", bad)
	}
}
