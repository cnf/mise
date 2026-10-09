package bom

import (
	"database/sql"
	"fmt"

	"example.com/cnf/mise/internal/store"
)

// ResolvedStep is a step with its sub-assembly/slot reference already followed,
// so the view does not have to. Children of an optional reference are attached
// only when the slot resolves to that assembly.
type ResolvedStep struct {
	store.Step
	ChosenLabel string       // for a slot reference: the option label
	Reference   *ResolvedMan // the Manual to follow after this step
}

type ResolvedMan struct {
	AssemblyID int64
	Title      string
	Steps      []ResolvedStep
	Loop       bool   // reference points back up the tree; do not follow
	Edit       bool   // render the editor rather than the read view
	Back       string // where this Manual's edit actions return to, mode included
}

// Manual resolves an assembly's steps for a given selection: a slot step is
// followed only when the slot resolves to an assembly, and its own steps are
// attached. Guards against a reference cycle by tracking the path.
func Manual(db *sql.DB, rootID int64, sel store.Selection) (*ResolvedMan, error) {
	if sel == nil {
		sel = store.Selection{}
	}
	a, err := store.GetAssembly(db, rootID)
	if err != nil || a == nil {
		return nil, fmt.Errorf("assembly %d not found", rootID)
	}
	m := &ResolvedMan{AssemblyID: rootID, Title: a.Name}
	m.Steps, err = resolveSteps(db, rootID, sel, map[int64]bool{rootID: true}, 0)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func resolveSteps(db *sql.DB, assemblyID int64, sel store.Selection, path map[int64]bool, depth int) ([]ResolvedStep, error) {
	if depth > 32 {
		return nil, nil
	}
	raw, err := store.Steps(db, assemblyID)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedStep, 0, len(raw))
	for _, st := range raw {
		rs := ResolvedStep{Step: st}
		switch st.ItemType {
		case "assembly":
			if st.ChildAssemblyID != nil {
				child, err := resolveChild(db, *st.ChildAssemblyID, sel, path, depth)
				if err != nil {
					return nil, err
				}
				rs.Reference = child
			}
		case "slot":
			if st.SlotID != nil {
				opt, _ := store.ChooseOption(db, *st.SlotID, sel)
				if opt != nil {
					rs.ChosenLabel = opt.Label
					if opt.TargetType == "assembly" && opt.ChildAssemblyID != nil {
						child, err := resolveChild(db, *opt.ChildAssemblyID, sel, path, depth)
						if err != nil {
							return nil, err
						}
						rs.Reference = child
					}
				}
			}
		}
		out = append(out, rs)
	}
	return out, nil
}

func resolveChild(db *sql.DB, id int64, sel store.Selection, path map[int64]bool, depth int) (*ResolvedMan, error) {
	a, err := store.GetAssembly(db, id)
	if err != nil || a == nil {
		return nil, nil
	}
	if path[id] {
		return &ResolvedMan{AssemblyID: id, Title: a.Name, Loop: true}, nil
	}
	path[id] = true
	defer delete(path, id)
	sub, err := resolveSteps(db, id, sel, path, depth+1)
	if err != nil {
		return nil, err
	}
	return &ResolvedMan{AssemblyID: id, Title: a.Name, Steps: sub}, nil
}

// PropagateMode pushes the render mode down the resolved tree. A sub-Manual
// reached through a reference is rendered by the same recursive template, so it
// needs the mode and the back-link too — otherwise a nested sub-Manual silently
// renders read-only while its parent is being edited.
func (m *ResolvedMan) PropagateMode(edit bool, back string) {
	for i := range m.Steps {
		if ref := m.Steps[i].Reference; ref != nil {
			ref.Edit = edit
			ref.Back = back
			ref.PropagateMode(edit, back)
		}
	}
}

// HasModel reports whether the resolved Manual contains an embedded 3D model
// anywhere in its tree, including nested sub-Manual references. The Manual page
// uses it to load the model-viewer bundle only when there is something to show,
// so the ~1 MB script is not fetched for a Manual of text and photos.
func (m *ResolvedMan) HasModel() bool {
	if m == nil {
		return false
	}
	for i := range m.Steps {
		s := &m.Steps[i]
		if s.Document != nil && s.Document.IsModel() {
			return true
		}
		if s.Reference != nil && s.Reference.HasModel() {
			return true
		}
	}
	return false
}
