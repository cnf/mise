package bom

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"example.com/cnf/mise/internal/store"
)

// ---------- Gather (the flatten) ----------

type GatheredPart struct {
	PartID    int64    `json:"part_id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Qty       float64  `json:"qty"`
	UnitPrice *float64 `json:"unit_price"`
	Currency  string   `json:"currency"`
	VendorURL string   `json:"vendor_url"`
	PartDBID  *int64   `json:"partdb_id"`
}

type GatherGroup struct {
	AssemblyID   int64          `json:"assembly_id"`
	AssemblyName string         `json:"assembly_name"`
	Lines        []GatheredPart `json:"lines"`
}

// ResolvedSlot records which option a slot contributed to the list, so the
// Gather view can show the build's choices and any compatibility note.
type ResolvedSlot struct {
	SlotID     int64  `json:"slot_id"`
	SlotName   string `json:"slot_name"`
	Assembly   string `json:"assembly"`
	Label      string `json:"label"`
	Target     string `json:"target"`
	Note       string `json:"note"`
	IsDefault  bool   `json:"is_default"`
	Unresolved bool   `json:"unresolved"`
}

type GatherResult struct {
	RootID    int64              `json:"root_id"`
	RootName  string             `json:"root_name"`
	Groups    []GatherGroup      `json:"groups"`
	Total     []GatheredPart     `json:"total"`
	TotalCost map[string]float64 `json:"total_cost"`
	Slots     []ResolvedSlot     `json:"slots"`
	Selection store.Selection    `json:"-"`
	Errors    []string           `json:"errors,omitempty"`
}

// Gather walks the assembly tree from rootID, summing part quantities and
// grouping them by the sub-assembly that declares them. Slot lines resolve to
// one option each: the build's selection if it has one, otherwise the option
// marked default, otherwise the first by sort order. DNP lines are excluded.
// Rollups are computed on read, never stored.
func Gather(db *sql.DB, rootID int64, sel store.Selection) (*GatherResult, error) {
	root, err := store.GetAssembly(db, rootID)
	if err != nil || root == nil {
		return nil, fmt.Errorf("assembly %d not found", rootID)
	}
	if sel == nil {
		sel = store.Selection{}
	}
	res := &GatherResult{RootID: rootID, RootName: root.Name, TotalCost: map[string]float64{}}

	type acc struct {
		name  string
		order int
		parts map[int64]*GatheredPart
	}
	groups := map[int64]*acc{}
	var groupOrder []int64
	totals := map[int64]*GatheredPart{}
	seen := map[int64]bool{}
	slotSeen := map[int64]bool{}
	order := 0

	addPart := func(g *acc, partID int64, name, kind string, price *float64, currency, vendor string, pdbid *int64, q float64) {
		if g.parts[partID] == nil {
			g.parts[partID] = &GatheredPart{PartID: partID, Name: name, Kind: kind, UnitPrice: price, Currency: currency, VendorURL: vendor, PartDBID: pdbid}
		}
		g.parts[partID].Qty += q
		if totals[partID] == nil {
			totals[partID] = &GatheredPart{PartID: partID, Name: name, Kind: kind, UnitPrice: price, Currency: currency, VendorURL: vendor, PartDBID: pdbid}
		}
		totals[partID].Qty += q
	}

	var walk func(id int64, mult float64, depth int)
	walk = func(id int64, mult float64, depth int) {
		if depth > 32 {
			res.Errors = append(res.Errors, fmt.Sprintf("depth limit reached at assembly %d (cycle?)", id))
			return
		}
		if seen[id] {
			res.Errors = append(res.Errors, fmt.Sprintf("assembly %d appears twice on the same path (cycle?)", id))
			return
		}
		seen[id] = true
		defer delete(seen, id)

		ls, err := store.Lines(db, id)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("assembly %d: %v", id, err))
			return
		}
		if _, ok := groups[id]; !ok {
			a, _ := store.GetAssembly(db, id)
			name := fmt.Sprintf("assembly %d", id)
			if a != nil {
				name = a.Name
			}
			groups[id] = &acc{name: name, order: order, parts: map[int64]*GatheredPart{}}
			groupOrder = append(groupOrder, id)
			order++
		}
		g := groups[id]

		for _, l := range ls {
			if l.DNP {
				continue
			}
			switch l.ItemType {
			case "part":
				if l.PartID == nil {
					continue
				}
				addPart(g, *l.PartID, l.PartName, l.PartKind, l.UnitPrice, l.Currency, l.VendorURL, l.PartDBID, l.Qty*mult)
			case "assembly":
				if l.ChildAssemblyID == nil {
					continue
				}
				walk(*l.ChildAssemblyID, mult*l.Qty, depth+1)
			case "slot":
				if l.SlotID == nil {
					continue
				}
				opt, isDefault := store.ChooseOption(db, *l.SlotID, sel)
				slotRow := ResolvedSlot{
					SlotID: *l.SlotID, SlotName: l.SlotName, Assembly: g.name,
					Note: l.Notes, IsDefault: isDefault,
				}
				if opt == nil {
					slotRow.Unresolved = true
					slotRow.Label = "(no options)"
					if !slotSeen[*l.SlotID] {
						slotSeen[*l.SlotID] = true
						res.Slots = append(res.Slots, slotRow)
					}
					res.Errors = append(res.Errors, fmt.Sprintf("slot %q in %s has no options", l.SlotName, g.name))
					continue
				}
				q := l.Qty * opt.Qty * mult
				switch opt.TargetType {
				case "part":
					if opt.PartID != nil {
						if p, err := store.GetPart(db, *opt.PartID); err == nil && p != nil {
							addPart(g, p.ID, p.Name, p.Kind, p.UnitPrice, p.Currency, p.VendorURL, p.PartDBID, q)
						}
					}
				case "assembly":
					if opt.ChildAssemblyID == nil {
						continue
					}
					walk(*opt.ChildAssemblyID, q, depth+1)
				}
				if !slotSeen[*l.SlotID] {
					slotSeen[*l.SlotID] = true
					slotRow.Label = opt.Label
					slotRow.Target = opt.TargetName()
					res.Slots = append(res.Slots, slotRow)
				}
			}
		}
	}
	walk(rootID, 1, 0)

	for _, id := range groupOrder {
		g := groups[id]
		gg := GatherGroup{AssemblyID: id, AssemblyName: g.name}
		for _, p := range g.parts {
			gg.Lines = append(gg.Lines, *p)
		}
		sort.Slice(gg.Lines, func(i, j int) bool {
			if gg.Lines[i].Kind != gg.Lines[j].Kind {
				return gg.Lines[i].Kind < gg.Lines[j].Kind
			}
			return strings.ToLower(gg.Lines[i].Name) < strings.ToLower(gg.Lines[j].Name)
		})
		res.Groups = append(res.Groups, gg)
	}

	for _, p := range totals {
		res.Total = append(res.Total, *p)
		if p.UnitPrice != nil {
			res.TotalCost[p.Currency] += *p.UnitPrice * p.Qty
		}
	}
	sort.Slice(res.Total, func(i, j int) bool {
		if res.Total[i].Kind != res.Total[j].Kind {
			return res.Total[i].Kind < res.Total[j].Kind
		}
		return strings.ToLower(res.Total[i].Name) < strings.ToLower(res.Total[j].Name)
	})
	sort.Slice(res.Slots, func(i, j int) bool { return res.Slots[i].SlotID < res.Slots[j].SlotID })
	return res, nil
}
