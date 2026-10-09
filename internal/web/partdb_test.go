package web

import (
	"encoding/json"
	"testing"

	"example.com/cnf/mise/internal/partdb"
)

// TestProviderReferenceShapes guards a real API quirk: providerReference comes
// back as a bare object for parts with one supplier reference and as an array
// for parts with several. Decoding it as one object aborts the whole sweep.
func TestProviderReferenceShapes(t *testing.T) {
	one := `{"id":1,"name":"BD139","providerReference":{"provider_key":"lcsc","provider_id":"C27866"}}`
	var p partdb.PdbPart
	if err := json.Unmarshal([]byte(one), &p); err != nil {
		t.Fatalf("single object form: %v", err)
	}
	if len(p.ProviderReference) != 1 || p.ProviderReference[0].ProviderKey != "lcsc" {
		t.Fatalf("single form decoded wrong: %+v", p.ProviderReference)
	}

	many := `{"id":2,"name":"X","providerReference":[{"provider_key":"mouser"},{"provider_key":"digikey"}]}`
	var q partdb.PdbPart
	if err := json.Unmarshal([]byte(many), &q); err != nil {
		t.Fatalf("array form: %v", err)
	}
	if len(q.ProviderReference) != 2 {
		t.Fatalf("array form decoded wrong: %+v", q.ProviderReference)
	}

	none := `{"id":3,"name":"Y","providerReference":null}`
	var r partdb.PdbPart
	if err := json.Unmarshal([]byte(none), &r); err != nil {
		t.Fatalf("null form: %v", err)
	}
	if len(r.ProviderReference) != 0 {
		t.Fatalf("null form should be empty, got %+v", r.ProviderReference)
	}
}
