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
- **Assembly** — a node with ordered lines. A line is either a **part** (qty *n*),
  a **sub-assembly** (qty *n*), or a **variant slot** (picks one option). Recursive,
  arbitrarily deep.
- **Variant slot** — a named point in an assembly where the builder picks one of
  several alternatives: *"this frame takes PCB rev A, B or C"*. Each option is a
  part or a sub-assembly, carries its own quantity, and one option is the default.
- **Build** — a saved release: a root assembly plus a choice per slot, frozen into
  an immutable snapshot so a unit built from it stays reproducible.

The **gather list** is the flatten of that tree: every part, quantities summed
through the sub-assembly multipliers and the chosen option's quantity, grouped by
the sub-assembly that declares it (which bag it belongs to), DNP lines excluded.
Computed on read, never stored. Freezing a build stores it.

**Variants vs versions.** Versions are the history of one node over time; variants
are concurrent alternatives at one point. Two builds can share one assembly tree and
differ only in which option each slot resolves to — that is what makes "the same
hardware, but rev C" a named, reproducible thing.

## The manual

Each assembly carries an ordered list of **steps**. A step is one of:

- **note** — a title, free text, and any number of images.
- **assembly** — "follow this sub-assembly's manual". Its steps are attached inline,
  so the manual tree is the same tree as the BOM. A reference that points back up
  the tree is marked and not followed, rather than recursing.
- **slot** — follow whichever variant the *build* chose. This is the seam between
  phases 2 and 3: the same manual reads differently for rev A and rev B.

Steps can be reordered (↑/↓, swapping sort order), and images are stored in the
database (capped at 10 MiB, png/jpeg/gif/webp only) and served from `/images/{id}`.

A `document` step embeds a KiCad InteractiveHtmlBom file: the board is stored whole
(capped at 32 MiB, and it must actually be an iBOM — the `pcbdata` blob has to be
present) and served from `/documents/{id}`, shown in a sandboxed iframe on the
manual page. Re-uploading replaces the stored board, so a re-exported layout lands
in place. The board is served with `Content-Security-Policy: sandbox allow-scripts`,
which puts it in an opaque origin: it can run its own scripts (it is a self-contained
page that renders the board with JavaScript) but cannot reach the parent page or the
network. This is the only place mise serves user-supplied markup, so the restriction
lives on that one response rather than globally.

Preview a manual as a specific build with `/manuals/{assembly_id}?build={build_id}`.
With no build it shows the assembly's own steps with variant references unresolved
to a selection, i.e. the slot's default.

## Migrations

`db.go` holds an ordered migration list; applied steps are recorded in a
`migrations` table. Steps that change a CHECK constraint rebuild the table
(SQLite cannot alter one in place), so an existing database upgrades in place
rather than needing a reset. `TestMigrationFromPhase1` proves a phase-1 database
keeps its rows through the upgrade.

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

- PDF export (deliberately never).
- A FreeCAD→glTF 3D viewer (nice-to-have, phase 5).
