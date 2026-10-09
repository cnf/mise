# mise

Lay out what you need, then build it.

A single-user tool for physical hardware projects: track a bill of materials that
spans sub-assemblies, and answer the two questions that matter —

- *"I'm building another one — what do I need to gather?"*
- *"I have the parts — show me how to do it again."*

Named for *mise en place*: everything laid out before you start.

## Model

Three levels:

- **Part** — a leaf you buy or make. Global, reusable across projects.
  Optionally linked to a [Part-DB](https://part-db.de/) part (read-only).
- **Assembly** — a node with ordered lines. A line is either a **part** (qty *n*)
  or a **sub-assembly** (qty *n*). Recursive, arbitrarily deep.
- **Build** — a saved release: a root assembly you freeze into an immutable
  snapshot so a unit built from it stays reproducible.

The **gather list** is the flatten of that tree: every part, quantities summed
through the sub-assembly multipliers, grouped by the sub-assembly that declares
it (which bag it belongs to), DNP lines excluded. Computed on read, never stored.
Freezing a build stores it.

## Running

Everything runs through devenv (Go toolchain + sqlite):

```sh
devenv shell --no-tui -- bash -c 'go build -o mise . && ./mise -db mise.db'
```

Then open <http://localhost:8080>.

Flags: `-addr` (default `:8080`), `-db` (default `mise.db`).

## Part-DB

Read-only. `mise` sweeps the whole part list into a local cache (`partdb_cache`)
and matches against that, because Part-DB's `/api/parts` filters on exact fields
only — there is no free-text query — and the dataset is small enough that a full
sweep is cheap and works offline afterwards. Press **Sync Part-DB** in the header.

Configuration comes from the environment (see `devenv.nix`):

| var | default |
|---|---|
| `PARTDB_BASE_URL` | `https://parts.home.nullsense.net/api` |
| `PARTDB_APIKEY_FILE` | `partdb_apikey` |

The key file is gitignored and never committed.

## iBOM import

KiCad's InteractiveHtmlBom output embeds an LZString-compressed JSON blob
(`pcbdata`) with the metadata, BOM and per-footprint geometry. `mise` decodes
that directly — no headless browser — and turns the board into an assembly of
its components (one part per distinct value+footprint, one line per refdes
group). DNP/skipped components are left out.

## Layout

```
main.go        HTTP handlers, routing, CSV export
db.go          schema + connection
store.go       parts, assemblies, lines, builds, the gather flatten
partdb.go      Part-DB cache sweep + search
ibom.go        InteractiveHtmlBom decode + assembly import
lzstring.go    LZString.decompressFromBase64 port
templates/     server-rendered pages (html/template)
```

## Not in this phase

Variants/slots (mixing e.g. one of several PCB revisions into a build), the
authored assembly manual, and PDF export. See
`/data/plans/assembly-bom-and-manual.md`.
