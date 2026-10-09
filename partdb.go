package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Part-DB is read-only from mise's side: we sweep its part list into a local
// cache and match against that. Its /api/parts endpoint filters on exact fields
// only (name, ipn, manufacturer_product_number, ...) with no free-text query,
// so per-keystroke live search is the wrong shape — and the instance is small
// enough (hundreds of parts) that a full sweep is trivial.

type pdbRef struct {
	ProviderKey string `json:"provider_key"`
	ProviderID  string `json:"provider_id"`
	ProviderURL string `json:"provider_url"`
}

// pdbRefList tolerates both shapes the API emits for providerReference: a bare
// object when a part has one supplier reference, and an array when it has
// several. Decoding it as a single object fails on the array case.
type pdbRefList []pdbRef

func (r *pdbRefList) UnmarshalJSON(b []byte) error {
	b = bytesTrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var list []pdbRef
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		*r = list
		return nil
	}
	var one pdbRef
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*r = []pdbRef{one}
	return nil
}

func bytesTrimSpace(b []byte) []byte {
	return bytes.TrimSpace(b)
}

type pdbNamed struct {
	Name string `json:"name"`
}

type pdbPart struct {
	ID                        int64      `json:"id"`
	Name                      string     `json:"name"`
	Description               string     `json:"description"`
	Manufacturer              *pdbNamed  `json:"manufacturer"`
	ManufacturerProductNumber string     `json:"manufacturer_product_number"`
	DatasheetURL              string     `json:"datasheet_url"`
	Footprint                 *pdbNamed  `json:"footprint"`
	Category                  *pdbNamed  `json:"category"`
	ProviderReference         pdbRefList `json:"providerReference"`
}

type pdbCollection struct {
	TotalItems int       `json:"hydra:totalItems"`
	Member     []pdbPart `json:"hydra:member"`
}

func partDBConfig() (base, key string, err error) {
	base = os.Getenv("PARTDB_BASE_URL")
	if base == "" {
		base = "https://parts.home.nullsense.net/api"
	}
	keyFile := os.Getenv("PARTDB_APIKEY_FILE")
	if keyFile == "" {
		keyFile = "partdb_apikey"
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return "", "", fmt.Errorf("read api key %s: %w", keyFile, err)
	}
	key = strings.TrimSpace(string(raw))
	if key == "" {
		return "", "", fmt.Errorf("api key file %s is empty", keyFile)
	}
	return strings.TrimRight(base, "/"), key, nil
}

// syncPartDB fetches every part from Part-DB and replaces the local cache.
func syncPartDB(db *sql.DB) (int, error) {
	base, key, err := partDBConfig()
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 30 * time.Second}

	fetched := 0
	page := 1
	const perPage = 250
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM partdb_cache`); err != nil {
		return 0, err
	}

	for {
		url := fmt.Sprintf("%s/parts?page=%d&itemsPerPage=%d", base, page, perPage)
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Accept", "application/ld+json")
		resp, err := client.Do(req)
		if err != nil {
			return fetched, fmt.Errorf("fetch page %d: %w", page, err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fetched, fmt.Errorf("part-db returned %s on page %d", resp.Status, page)
		}
		var col pdbCollection
		if err := json.Unmarshal(body, &col); err != nil {
			return fetched, fmt.Errorf("decode page %d: %w", page, err)
		}
		if len(col.Member) == 0 {
			break
		}
		for _, p := range col.Member {
			mpn := p.ManufacturerProductNumber
			manu := ""
			if p.Manufacturer != nil {
				manu = p.Manufacturer.Name
			}
			desc := p.Description
			if p.Category != nil && p.Category.Name != "" {
				desc = strings.TrimSpace(p.Category.Name + " " + desc)
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO partdb_cache (partdb_id,name,mpn,manufacturer,description,datasheet_url,fetched_at)
				VALUES (?,?,?,?,?,?,datetime('now'))`, p.ID, p.Name, mpn, manu, desc, p.DatasheetURL); err != nil {
				return fetched, err
			}
			fetched++
		}
		if fetched >= col.TotalItems || len(col.Member) < perPage {
			break
		}
		page++
	}
	if err := tx.Commit(); err != nil {
		return fetched, err
	}
	return fetched, nil
}

type CacheHit struct {
	PartDBID     int64  `json:"partdb_id"`
	Name         string `json:"name"`
	MPN          string `json:"mpn"`
	Manufacturer string `json:"manufacturer"`
	Description  string `json:"description"`
	DatasheetURL string `json:"datasheet_url"`
}

func searchCache(db *sql.DB, q string) ([]CacheHit, error) {
	q = strings.TrimSpace(q)
	sqlStr := `SELECT partdb_id,name,mpn,manufacturer,description,datasheet_url FROM partdb_cache`
	var args []any
	if q != "" {
		sqlStr += ` WHERE name LIKE ? OR mpn LIKE ? OR description LIKE ?`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	sqlStr += ` ORDER BY name COLLATE NOCASE LIMIT 25`
	rows, err := db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CacheHit{}
	for rows.Next() {
		var h CacheHit
		if err := rows.Scan(&h.PartDBID, &h.Name, &h.MPN, &h.Manufacturer, &h.Description, &h.DatasheetURL); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
