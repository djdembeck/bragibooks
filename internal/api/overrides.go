package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bragibooks/bragibooks/internal/audio"
)

// This file is the manual-metadata-override half of the editing workflow
// (B3). The book_snapshots row (snapshot.go) captures the selected catalog
// record; this table records the operator's EXPLICIT per-field intent on
// top of it: an edited value, an explicit clear (remove the embedded tag
// from the output), or no opinion (the catalog value applies). The two
// representations are deliberately separate: re-matching re-takes the
// capture, and the override layer must survive that untouched, while
// "reset to catalog" is an explicit operator action that deletes the
// override — the opposite of clearing a tag.
//
// Effective metadata = catalog capture with the override layer applied
// (applyMetadataOverride). Only the effective values name the output and
// feed the selected writes; a cleared field is blanked from the effective
// metadata AND pinned to the engine's clear policy, so its tag is removed
// from the output even though the write selection still lists it.

// MetadataOverride is the durable per-book override row. Per-field storage
// conventions (the zero value is "no override" for every field):
//
//   - scalar text (Title, Subtitle, SeriesName, SeriesPos, Description):
//     ” = no override. A blank is a legal CATALOG state (e.g. no
//     subtitle), so "clear the tag" for a scalar text field is not the
//     empty string — it is the field's entry in Cleared.
//   - arrays (Authors, Narrators, Genres): nil = no override, a non-nil
//     slice (possibly empty) = the edited value; an explicit clear is the
//     Cleared entry (saving an empty list as an EDIT is legal — it means
//     "no people" — and is distinct from clearing the tag).
//   - Year: nil = no override, a value = the edited year; an explicit
//     clear is the Cleared entry (the column alone cannot carry all three
//     states).
//   - Cleared: the field names the operator explicitly cleared (remove
//     the embedded tag from the output). Vocabulary: the scalar fields
//     plus "cover" and "chapters". Reset-to-catalog (deleting the row)
//     is distinct from a clear entry and is never conflated with it.
//   - CoverSource: ” = no override (the catalog cover applies),
//     'clear' = remove the cover from the output, 'file' = the
//     operator's own artwork in CoverFile (an immutable local asset under
//     the covers directory), 'url' = the operator's own cover URL in
//     CoverURL (validated at save time with the same public-URL rules as
//     catalog cover fetches; the engine embeds it at run time).
//   - Chapters: nil = no override (the selected release's chapters and
//     the chapter write policy apply), a non-nil slice (possibly empty =
//     remove the chapters from the output) = the operator's explicit
//     list, integer-millisecond times in the engine's wire shape.
type MetadataOverride struct {
	BookID      int64             `json:"-"`
	Title       string            `json:"title"`
	Subtitle    string            `json:"subtitle"`
	Authors     []string          `json:"authors"`
	Narrators   []string          `json:"narrators"`
	SeriesName  string            `json:"series_name"`
	SeriesPos   string            `json:"series_position"`
	Description string            `json:"description"`
	Genres      []string          `json:"genres"`
	Year        *int              `json:"year"`
	Cleared     []string          `json:"cleared"`
	CoverSource string            `json:"cover_source"`
	CoverFile   string            `json:"cover_file"`
	CoverURL    string            `json:"cover_url"`
	Chapters    []SnapshotChapter `json:"chapters"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
}

// overrideFieldVocabulary is every field name a Cleared entry may carry.
var overrideFieldVocabulary = map[string]bool{
	"title": true, "subtitle": true, "authors": true, "narrators": true,
	"series": true, "description": true, "genres": true, "year": true,
	"cover": true, "chapters": true,
}

// validOverrideField reports whether name is a legal Cleared entry.
func validOverrideField(name string) bool {
	return overrideFieldVocabulary[name]
}

// normalizeClearedFields canonicalizes the explicit-clear set before it is
// stored or applied: it dedupes entries and folds an explicit EMPTY chapter
// list into cleared:["chapters"]. The empty-list form and the cleared-form
// are the same operator intent (the engine's explicit chapter clear), so a
// request may express it either way; the stored row and the applied
// effective state agree on one spelling. A NON-EMPTY list is an edited value,
// not a clear: it drops any "chapters" entry, because a saved clear must not
// survive the operator returning to the catalog list or adding custom
// chapters — the clear intent is then gone (a stale marker would discard the
// edited list and keep the tags removed with no way to recover short of a
// full reset).
func normalizeClearedFields(cleared []string, chapters []SnapshotChapter) []string {
	out := make([]string, 0, len(cleared)+1)
	seen := map[string]bool{}
	for _, c := range cleared {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if chapters != nil && len(chapters) > 0 {
		out = out[:0]
		seen = map[string]bool{}
		for _, c := range cleared {
			if c == "chapters" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	} else if chapters != nil && len(chapters) == 0 && !seen["chapters"] {
		out = append(out, "chapters")
	}
	sort.Strings(out)
	return out
}

// maxOverrideCoverUploadBytes caps a manual cover upload at the PINNED
// engine's contract: m4b-merge v2.2.0 rejects local cover art over 5 MiB,
// so accepting 8 MiB here would stage a run that fails only at run time.
// The catalog capture path shares the same limit (coverMaxBytes) — a cover
// is a thumbnail, not a book, on both paths.
const maxOverrideCoverUploadBytes = 5 << 20

// maxOverrideUploadBodyBytes caps the WHOLE multipart request body (the
// file limit plus headroom for the form framing and the non-file field cap)
// BEFORE multipart parsing: ParseMultipartForm's memory cap does not limit
// total size, so the transport cap is what bounds an oversized upload.
const maxOverrideUploadBodyBytes = maxOverrideCoverUploadBytes + 256<<10

// maxOverrideChapters caps a manual chapter list: a legitimate audiobook
// has far fewer than this, and the cap bounds the JSON round trip and the
// engine's chapter write.
const maxOverrideChapters = 1000

// maxMetadataRequestBodyBytes caps the WHOLE editor PUT body before it is
// decoded: the per-field caps bound the content, but the transport is only
// bounded here, so an authenticated caller cannot stream an unbounded JSON
// body into the decoder. The ceiling is well above the largest legal draft
// (1000 chapters × 500-char titles + a 20 KB description ≈ 0.6 MiB), so a
// valid save is never rejected by the transport cap.
const maxMetadataRequestBodyBytes = 2 << 20

// randRead is crypto/rand.Read, var-bound so tests may substitute a
// deterministic source.
var randRead = rand.Read

// ── Storage ───────────────────────────────────────────────────────────

// loadMetadataOverride reads the book's override row, or nil when no
// overrides exist.
func (h *Handler) loadMetadataOverride(bookID int64) (*MetadataOverride, error) {
	return loadBookMetadataOverride(h.svc.DB, bookID)
}

// loadBookMetadataOverride is the shared database read for a book's manual
// overrides, usable by the API handler and the processing worker.
func loadBookMetadataOverride(db *sql.DB, bookID int64) (*MetadataOverride, error) {
	var (
		bookIDCol int64
		year      sql.NullInt64
		updatedAt sql.NullString
	)
	var (
		title, subtitle, authorsJSON, narratorsJSON, seriesName, seriesPos string
		description, genresJSON, clearedJSON                               string
		coverSource, coverFile, coverURL, chaptersJSON                     string
	)
	err := db.QueryRow(
		`SELECT book_id, title, subtitle, authors, narrators, series_name, series_position,
		        description, genres, year, cleared, cover_source, cover_file, cover_url, chapters, updated_at
		 FROM book_metadata_overrides WHERE book_id = ?`,
		bookID,
	).Scan(&bookIDCol, &title, &subtitle, &authorsJSON, &narratorsJSON,
		&seriesName, &seriesPos, &description, &genresJSON, &year,
		&clearedJSON, &coverSource, &coverFile, &coverURL, &chaptersJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load metadata overrides: %w", err)
	}
	o := &MetadataOverride{
		BookID: bookIDCol, Title: title, Subtitle: subtitle,
		SeriesName: seriesName, SeriesPos: seriesPos, Description: description,
		CoverSource: coverSource, CoverFile: coverFile, CoverURL: coverURL,
		UpdatedAt: updatedAt.String,
	}
	if year.Valid {
		v := int(year.Int64)
		o.Year = &v
	}
	// The array columns store JSON only when an override exists ('' = no
	// override, '[]' = an explicit empty list). A decode failure means the
	// stored value is corrupt and must surface as a load error: folding it
	// into an empty slice would turn it into an explicit clear and silently
	// blank the captured values on the next save/job.
	if o.Authors, err = jsonStrings(authorsJSON); err != nil {
		return nil, fmt.Errorf("load metadata overrides: authors: %w", err)
	}
	if o.Narrators, err = jsonStrings(narratorsJSON); err != nil {
		return nil, fmt.Errorf("load metadata overrides: narrators: %w", err)
	}
	if o.Genres, err = jsonStrings(genresJSON); err != nil {
		return nil, fmt.Errorf("load metadata overrides: genres: %w", err)
	}
	if o.Cleared, err = jsonStrings(clearedJSON); err != nil {
		return nil, fmt.Errorf("load metadata overrides: cleared: %w", err)
	}
	if o.Chapters, err = jsonChapters(chaptersJSON); err != nil {
		return nil, fmt.Errorf("load metadata overrides: chapters: %w", err)
	}
	return o, nil
}

// jsonStrings decodes the stored array convention: "" is the no-override
// state (nil), and any other content MUST decode to a slice ('[]' = an
// explicit empty list). A decode failure is surfaced, NOT folded into an
// empty slice: '[]' is the explicit-empty ("clear the tags") state, so a
// corrupted column must never masquerade as an operator's explicit clear —
// that would silently replace captured people/genres with nothing.
func jsonStrings(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode %q: %w", raw, err)
	}
	if out == nil {
		// A literal JSON null decodes into a nil slice WITHOUT an error. It
		// is stored state, not a valid operator draft ('[]' is the explicit
		// empty), so treat it as corrupt rather than "no override".
		return nil, fmt.Errorf("decode %q: stored null is not a valid array", raw)
	}
	return out, nil
}

// jsonChapters decodes the stored chapters convention ("" = no override,
// any other content = the explicit list, '[]' = clear chapters). A decode
// failure is surfaced for the same reason jsonStrings surfaces its own:
// the no-override and explicit-empty states are distinct and must not be
// conflated by a corrupt value.
func jsonChapters(raw string) ([]SnapshotChapter, error) {
	if raw == "" {
		return nil, nil
	}
	var out []SnapshotChapter
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode %q: %w", raw, err)
	}
	if out == nil && raw != "[]" {
		// '[]' is the legitimate explicit-clear state and decodes to an
		// empty (non-nil) slice; a literal null decodes to nil WITHOUT an
		// error and is corrupt state, not "no override".
		return nil, fmt.Errorf("decode %q: stored null is not a valid array", raw)
	}
	return out, nil
}

// saveMetadataOverride persists the row (insert-or-replace on book_id).
// An all-blank override is DELETED instead: such a row is
// indistinguishable from "no overrides" on the next load, and the
// reset-to-catalog action is exactly that deletion.
func (h *Handler) saveMetadataOverride(o *MetadataOverride) error {
	if o == nil || overrideIsEmpty(o) {
		if _, err := h.svc.DB.Exec("DELETE FROM book_metadata_overrides WHERE book_id = ?", o.BookID); err != nil {
			return fmt.Errorf("delete metadata overrides: %w", err)
		}
		return nil
	}
	// The optional arrays follow the same storage convention as chapters
	// below: nil (no override) stores the empty string, and a non-nil slice
	// — INCLUDING an explicit empty [] — stores its JSON. This is load-bearing
	// (finding: nil arrays persisted as "[]"): a field the operator did not
	// edit must round-trip to nil so applyMetadataOverride keeps the catalog
	// value, while an edited value (even a deliberate empty list) stores its
	// JSON so it reads back non-nil. nonNilStrings would collapse nil into []
	// and the catalog people/genres would be silently blanked on the first
	// edit of any other field.
	authors := marshalOptionalStrings(o.Authors)
	narrators := marshalOptionalStrings(o.Narrators)
	genres := marshalOptionalStrings(o.Genres)
	cleared := marshalOptionalStrings(o.Cleared)
	chapters := ""
	if o.Chapters != nil {
		b, _ := json.Marshal(o.Chapters)
		chapters = string(b)
	}
	var year sql.NullInt64
	if o.Year != nil {
		year = sql.NullInt64{Int64: int64(*o.Year), Valid: true}
	}
	_, err := h.svc.DB.Exec(
		`INSERT INTO book_metadata_overrides (book_id, title, subtitle, authors, narrators,
	   series_name, series_position, description, genres, year, cleared, cover_source,
	   cover_file, cover_url, chapters, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	 ON CONFLICT(book_id) DO UPDATE SET
	   title=excluded.title, subtitle=excluded.subtitle,
	   authors=excluded.authors, narrators=excluded.narrators,
	   series_name=excluded.series_name, series_position=excluded.series_position,
	   description=excluded.description, genres=excluded.genres, year=excluded.year,
	   cleared=excluded.cleared, cover_source=excluded.cover_source,
	   cover_file=excluded.cover_file, cover_url=excluded.cover_url,
	   chapters=excluded.chapters, updated_at=datetime('now')`,
		o.BookID, o.Title, o.Subtitle, authors, narrators,
		o.SeriesName, o.SeriesPos, o.Description, genres, year,
		cleared, o.CoverSource, o.CoverFile, o.CoverURL, chapters,
	)
	if err != nil {
		return fmt.Errorf("save metadata overrides: %w", err)
	}
	return nil
}

// marshalOptionalStrings serializes the optional-array storage convention:
// nil (no override) is the empty string, a non-nil slice — including an
// explicit empty one — is its JSON ('[]' for the empty case). The decoder
// (jsonStrings/jsonChapters) is the exact inverse: ” -> nil, '[]' -> an
// empty non-nil slice, anything else -> the list.
func marshalOptionalStrings(in []string) string {
	if in == nil {
		return ""
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// overrideIsEmpty reports whether every field is at its "no override"
// zero state (the row can then be deleted instead of written).
func overrideIsEmpty(o *MetadataOverride) bool {
	return o.Title == "" && o.Subtitle == "" && o.Authors == nil &&
		o.Narrators == nil && o.SeriesName == "" && o.SeriesPos == "" &&
		o.Description == "" && o.Genres == nil && o.Year == nil &&
		len(o.Cleared) == 0 && o.CoverSource == "" && o.CoverFile == "" &&
		o.CoverURL == "" && o.Chapters == nil
}

// deleteMetadataOverride removes the book's override row (reset to
// catalog). Idempotent: a book with no row stays without one.
func (h *Handler) deleteMetadataOverride(bookID int64) error {
	if _, err := h.svc.DB.Exec("DELETE FROM book_metadata_overrides WHERE book_id = ?", bookID); err != nil {
		return fmt.Errorf("delete metadata overrides: %w", err)
	}
	return nil
}

// ── Effective state ───────────────────────────────────────────────────

// overridePinnedPolicies are the engine write policies a manual override
// FORCES on top of the settings-based selection. Nil = the override pins
// nothing (the selection and effective metadata derive the policy as
// before). Scalars carry ONLY the cleared fields at "clear" (an explicit
// clear is the editor's only scalar policy-level operation; an edited
// value keeps the selection's write/preserve decision, and the
// non-cleared fields keep the baseline derived from the selection).
// Cover/Chapters pin one of the engine's policy strings
// (write/clear/write_url for cover, write/clear for chapters). A frozen
// job carries these in its WriteSelection, so a queued job's intent
// cannot drift under later settings or catalog changes.
type overridePinnedPolicies struct {
	Scalars  *audio.WritePolicies
	Cover    *string
	Chapters *string
}

// applyMetadataOverride returns the catalog capture with the operator's
// override layer applied: the effective metadata a job will name from,
// plus the engine policies the override forces (nil when the override
// pins nothing — the caller then derives policies from the write
// selection and the effective metadata as before).
//
// Rules:
//   - A present edited value replaces the catalog value for naming;
//     writing it still follows the settings-based write selection (an
//     edit never auto-enables a write).
//   - A Cleared entry blanks the effective value AND pins that field's
//     engine policy to "clear", independent of the write selection:
//     clearing a tag is an explicit operator operation, not a side
//     effect of a settings checkbox.
//   - Cover: 'clear' pins the cover policy to "clear"; 'file' replaces
//     the captured cover reference with the operator's asset; 'url' pins
//     the engine's write_url policy (embedded at run time, validated
//     public-URL at save).
//   - Chapters: an explicit clear (an empty list, or cleared:["chapters"])
//     removes the chapters and pins the chapter policy to "clear" (the one
//     unconditional chapter pin — it overrides a saved write policy). A
//     non-empty edited list replaces the captured chapters for naming and
//     run-time writes, but the chapter write POLICY stays with the saved
//     per-field selection: a "preserve" choice keeps its no-embed /
//     no-sidecar behavior even when the list has been edited.
//
// The returned BookSnapshot is a COPY: the queue path freezes it into
// the job row, and caller mutations must not leak into the stored
// capture.
func applyMetadataOverride(snap *BookSnapshot, o *MetadataOverride) (BookSnapshot, *overridePinnedPolicies) {
	eff := *snap
	if o == nil {
		return eff, nil
	}
	if o.Title != "" {
		eff.Title = o.Title
	}
	if o.Subtitle != "" {
		eff.Subtitle = o.Subtitle
	}
	if o.Authors != nil {
		eff.Authors = append([]string(nil), o.Authors...)
	}
	if o.Narrators != nil {
		eff.Narrators = append([]string(nil), o.Narrators...)
	}
	if o.SeriesName != "" {
		eff.SeriesName = o.SeriesName
	}
	if o.SeriesPos != "" {
		eff.SeriesPosition = o.SeriesPos
	}
	if o.Description != "" {
		eff.Description = o.Description
	}
	if o.Genres != nil {
		eff.Genres = append([]string(nil), o.Genres...)
	}
	if o.Year != nil {
		y := *o.Year
		eff.Year = &y
	}
	if o.CoverSource == "file" && o.CoverFile != "" {
		eff.CoverFile = o.CoverFile
	}
	if o.Chapters != nil {
		list := make([]SnapshotChapter, len(o.Chapters))
		copy(list, o.Chapters)
		eff.Chapters = list
	}

	clearSet := map[string]bool{}
	for _, c := range o.Cleared {
		clearSet[c] = true
	}
	pinned := &overridePinnedPolicies{}
	anyScalarClear := false
	if clearSet["title"] || clearSet["subtitle"] || clearSet["authors"] ||
		clearSet["narrators"] || clearSet["series"] || clearSet["description"] ||
		clearSet["genres"] || clearSet["year"] {
		anyScalarClear = true
	}
	if anyScalarClear {
		// The pinned scalar policies carry ONLY the cleared fields
		// (the rest stay at the zero value ""); buildEngineJob applies
		// the pins on top of the write-selection-derived baseline, so an
		// edited value keeps the selection's write/preserve decision and
		// the cleared fields are authoritative "clear".
		//
		// A cleared field's EFFECTIVE value is deliberately NOT blanked
		// (finding: clear vs naming). The effective snapshot is what names
		// the output (toSupplied -> the engine's path resolver) and what a
		// frozen job carries; blanking it here made a cleared title/series/
		// year produce an empty naming segment while the editor promised
		// the value is kept. The engine's "clear" policy already removes
		// the tag atom at run time, so the value stays for naming and the
		// policy removes the tag — no field needs to be blanked to clear it.
		p := audio.WritePolicies{}
		if clearSet["title"] {
			p.Title = "clear"
		}
		if clearSet["subtitle"] {
			p.Subtitle = "clear"
		}
		if clearSet["authors"] {
			p.Authors = "clear"
		}
		if clearSet["narrators"] {
			p.Narrators = "clear"
		}
		if clearSet["series"] {
			p.Series = "clear"
		}
		if clearSet["description"] {
			p.Description = "clear"
		}
		if clearSet["genres"] {
			p.Genres = "clear"
		}
		if clearSet["year"] {
			p.Year = "clear"
		}
		pinned.Scalars = &p
	}
	if clearSet["cover"] || o.CoverSource == "clear" {
		eff.CoverFile = ""
		pinned.Cover = new("clear")
	} else if o.CoverSource == "url" && o.CoverURL != "" {
		// write_url: the engine embeds the URL at run time (the
		// operator's own cover, the same trusted-URL surface catalog
		// covers use; validated public-URL at save time).
		pinned.Cover = new("write_url")
	}
	if clearSet["chapters"] {
		// An explicit chapters clear (cleared:["chapters"], or an empty
		// list, which is the same intent): remove the embedded chapters.
		// This is the ONE unconditional pin — it overrides a saved
		// chapter-write policy, because removing the tags is an explicit
		// operator operation, not a side effect of a settings checkbox.
		// The effective value is a NON-NIL empty list, the documented
		// override-list form: nil means "no override, the catalog
		// chapters apply", so an explicit clear must never read back as
		// the catalog.
		eff.Chapters = []SnapshotChapter{}
		pinned.Chapters = new("clear")
	} else if o.Chapters != nil && len(o.Chapters) > 0 {
		// A non-empty edited list is effective METADATA (the engine names
		// the output and, at run time, writes the chapters from it). It does
		// NOT force the chapter write policy: that stays with the saved
		// per-field selection, so a "preserve" choice keeps its no-embed /
		// no-sidecar behavior even when the operator has edited the list.
		// Forcing "write" here would embed + generate the sidecar despite
		// the operator's preserve intent (plan: preserving chapter writes
		// must not trigger unwanted embed/sidecar mutation).
	}
	if !anyScalarClear && pinned.Cover == nil && pinned.Chapters == nil {
		return eff, nil
	}
	return eff, pinned
}

// ── API handlers: the manual metadata editor ──────────────────────────
//
// GET  /api/books/{id}/metadata   — snapshot + stored overrides +
//                                   server-computed effective state (the
//                                   single source of truth for the
//                                   editor UI).
// PUT  /api/books/{id}/metadata   — save the editor's full override draft
//                                   (validate, persist the row, truthful
//                                   audit entry).
// POST /api/books/{id}/metadata/reset — delete the override row (reset to
//                                   catalog: the selected release is
//                                   untouched), prune orphaned override
//                                   covers, audit.
// POST /api/books/{id}/metadata/cover — upload the operator's own
//                                   artwork as an immutable local asset;
//                                   the editor then references it in the
//                                   override draft.
//
// Saving overrides NEVER writes output files: tags are only touched by a
// processing job, and the audit detail says exactly that. A queued job is
// frozen at enqueue time (effective values + pinned policies + asset
// references), so a later edit changes future jobs only.

// bookMetadataRequest is the editor's full override draft. Field
// conventions mirror MetadataOverride: scalar strings ” = no override;
// people/genres null = no override, non-nil (possibly empty) = the edited
// list; year null = no override, a number = the edited year; cleared =
// the explicit tag-removal set; cover_source ” | 'clear' | 'file' |
// 'url'; chapters null = catalog, [] = clear, a list = the explicit list.
type bookMetadataRequest struct {
	Title       string            `json:"title"`
	Subtitle    string            `json:"subtitle"`
	Authors     []string          `json:"authors"`
	Narrators   []string          `json:"narrators"`
	SeriesName  string            `json:"series_name"`
	SeriesPos   string            `json:"series_position"`
	Description string            `json:"description"`
	Genres      []string          `json:"genres"`
	Year        *int              `json:"year"`
	Cleared     []string          `json:"cleared"`
	CoverSource string            `json:"cover_source"`
	CoverFile   string            `json:"cover_file"`
	CoverURL    string            `json:"cover_url"`
	Chapters    []SnapshotChapter `json:"chapters"`
}

// bookMetadataResponse is the editor's view of one book's metadata state.
// Effective is the post-override state the engine will name from and run
// under — computed server-side so the UI never carries a parallel merge.
// EffectiveChapters is nil when the catalog chapters apply (the UI shows
// the catalog list), and non-nil (possibly empty = clear) for an
// override-driven list.
type bookMetadataResponse struct {
	Snapshot          *BookSnapshot     `json:"snapshot"`
	Overrides         *MetadataOverride `json:"overrides"`
	Effective         *BookSnapshot     `json:"effective"`
	EffectiveChapters []SnapshotChapter `json:"effective_chapters"`
}

// ovChapterOverride reports whether the override pins the chapter list at
// all: an explicit list (o.Chapters, possibly the empty clear list) or the
// normalized clear marker in o.Cleared. It is the SAME test
// applyMetadataOverride uses to decide the effective chapters, so the
// response's EffectiveChapters can never disagree with what a queued job
// runs under.
func ovChapterOverride(o *MetadataOverride) bool {
	if o.Chapters != nil {
		return true
	}
	for _, c := range o.Cleared {
		if c == "chapters" {
			return true
		}
	}
	return false
}

// bookMetadataResponse builds the editor state (snapshot + overrides +
// effective) for one book.
func (h *Handler) bookMetadataResponse(bookID int64) (*bookMetadataResponse, error) {
	snap, err := h.loadSnapshot(bookID)
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	ov, err := h.loadMetadataOverride(bookID)
	if err != nil {
		return nil, fmt.Errorf("load metadata overrides: %w", err)
	}
	resp := &bookMetadataResponse{Snapshot: snap, Overrides: ov}
	if snap != nil {
		eff, _ := applyMetadataOverride(snap, ov)
		resp.Effective = &eff
		// Chapters follow the documented contract: nil = the catalog
		// chapters apply, non-nil = the override list (possibly empty =
		// clear). The EFFECTIVE snapshot's Chapters is the catalog slice
		// whenever no chapter override exists, so the response field is
		// derived from the OVERRIDE, not the effective value — otherwise
		// a no-override book would report a non-nil list and read as a
		// catalog-applies state inverted. A cleared:["chapters"] pin is an
		// override too (ov.Chapters stays nil, the clear rides in
		// ClearedFields): it must report the non-nil empty effective list,
		// not nil, or the response claims the catalog chapters apply while
		// the queued job is pinned to clear.
		if ov != nil && ovChapterOverride(ov) {
			resp.EffectiveChapters = eff.Chapters
		}
	}
	return resp, nil
}

// bookIDParam extracts the {id} URL parameter, writing the 400/404
// response and returning ok=false when it is missing or unknown.
func (h *Handler) bookIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	bookID := parseInt64Param(chiURLParam(r, "id"))
	if bookID == -1 {
		writeError(w, http.StatusBadRequest, "invalid book ID")
		return 0, false
	}
	var exists int
	if err := h.svc.DB.QueryRow("SELECT 1 FROM books WHERE id = ?", bookID).Scan(&exists); err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "book not found")
		return 0, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "load book: "+err.Error())
		return 0, false
	}
	return bookID, true
}

// GetBookMetadata serves the editor state for one book.
func (h *Handler) GetBookMetadata(w http.ResponseWriter, r *http.Request) {
	bookID, ok := h.bookIDParam(w, r)
	if !ok {
		return
	}
	resp, err := h.bookMetadataResponse(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateBookMetadata saves the editor's override draft.
func (h *Handler) UpdateBookMetadata(w http.ResponseWriter, r *http.Request) {
	bookID, ok := h.bookIDParam(w, r)
	if !ok {
		return
	}
	// The draft is bounded BEFORE decoding: an authenticated caller must not
	// be able to stream an unbounded JSON body into the decoder (memory) —
	// the per-field caps below bound the CONTENT, not the transport.
	r.Body = http.MaxBytesReader(w, r.Body, maxMetadataRequestBodyBytes)
	var req bookMetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("metadata request exceeds the %d KB limit", maxMetadataRequestBodyBytes>>10))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid metadata request: "+err.Error())
		return
	}
	o := &MetadataOverride{
		BookID: bookID, Title: req.Title, Subtitle: req.Subtitle,
		Authors: req.Authors, Narrators: req.Narrators,
		SeriesName: req.SeriesName, SeriesPos: req.SeriesPos,
		Description: req.Description, Genres: req.Genres, Year: req.Year,
		Cleared: req.Cleared, CoverSource: req.CoverSource,
		CoverFile: req.CoverFile, CoverURL: req.CoverURL, Chapters: req.Chapters,
	}
	o.Cleared = normalizeClearedFields(o.Cleared, o.Chapters)
	if err := validateMetadataOverride(r.Context(), o); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A historical book (no capture row) must become queueable from the
	// editor: capture the catalog row once, degraded-catalog-tolerant and
	// WITHOUT a catalog round trip (a manual edit never changes catalog
	// content, so changed=false). An existing row is left untouched — its
	// captured_at is the capture's own bookkeeping, not the editor's.
	if snap, err := h.loadSnapshot(bookID); err != nil {
		writeError(w, http.StatusInternalServerError, "load snapshot: "+err.Error())
		return
	} else if snap == nil {
		if _, err := h.captureSnapshot(r.Context(), bookID, false); err != nil {
			writeError(w, http.StatusInternalServerError, "capture snapshot: "+err.Error())
			return
		}
	}
	// The row mutation and the orphan-cover prune are ONE critical section
	// under the per-book lock (the same lock capture and enqueue hold):
	// the keep-set is read from the row AS JUST WRITTEN, so a concurrent
	// save/reset cannot leave the row pointing at an asset a stale prune
	// then deletes (finding: prune vs save). The lock is released before
	// the audit insert and the response build, which do not touch covers.
	unlock := h.svc.ProcessingSvc.lockBookCover(bookID)
	if err := h.saveMetadataOverride(o); err != nil {
		unlock()
		writeError(w, http.StatusInternalServerError, "save metadata overrides: "+err.Error())
		return
	}
	// The row about to be pruned against is now the authoritative reference
	// for this book's covers; drop the pending-upload entries so this save's
	// own sweep prunes an upload the saved row does NOT reference (an upload
	// that IS referenced stays via the row's keep-set).
	h.svc.dropPendingCoversFor(bookID)
	if keepFile, ok := h.persistedOverrideCoverFile(bookID); ok {
		h.pruneOrphanedOverrideCovers(bookID, keepFile)
	} else {
		// The keep-set read failed: prune against no keep-set could delete
		// the asset the saved row references, so skip the sweep this time.
	}
	unlock()
	var title string
	if err := h.svc.DB.QueryRow("SELECT title FROM books WHERE id = ?", bookID).Scan(&title); err != nil {
		title = fmt.Sprintf("%d", bookID)
	}
	h.svc.ProcessingSvc.RecordBookAction(bookID, "metadata_update",
		"Edited manual metadata for \""+title+"\" — "+overrideChangeSummary(o)+
			" (database record only; tags are written when a processing job runs)")
	resp, err := h.bookMetadataResponse(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ResetBookMetadata deletes the book's override row: the catalog capture
// (the selected release and its values) is untouched — reset-to-catalog
// is the distinct operator action that discards the manual layer, not a
// clear-tag intent.
func (h *Handler) ResetBookMetadata(w http.ResponseWriter, r *http.Request) {
	bookID, ok := h.bookIDParam(w, r)
	if !ok {
		return
	}
	// The row deletion and the orphan-cover prune are ONE critical section
	// under the per-book lock (the same lock capture and enqueue hold):
	// after the delete the row references no asset, so the keep-set is
	// empty apart from any active job's queued references, and a concurrent
	// save cannot race the delete to leave a stale prune (finding: prune vs
	// save). The lock is released before the capture and the audit insert.
	unlock := h.svc.ProcessingSvc.lockBookCover(bookID)
	if err := h.deleteMetadataOverride(bookID); err != nil {
		unlock()
		writeError(w, http.StatusInternalServerError, "reset metadata: "+err.Error())
		return
	}
	h.pruneOrphanedOverrideCovers(bookID, "")
	unlock()
	// Guarantee the capture row's existence (the reset must not strand a
	// historical book without one).
	if snap, err := h.loadSnapshot(bookID); err != nil {
		writeError(w, http.StatusInternalServerError, "load snapshot: "+err.Error())
		return
	} else if snap == nil {
		if _, err := h.captureSnapshot(r.Context(), bookID, false); err != nil {
			writeError(w, http.StatusInternalServerError, "capture snapshot: "+err.Error())
			return
		}
	}
	var title string
	if err := h.svc.DB.QueryRow("SELECT title FROM books WHERE id = ?", bookID).Scan(&title); err != nil {
		title = fmt.Sprintf("%d", bookID)
	}
	h.svc.ProcessingSvc.RecordBookAction(bookID, "metadata_update",
		"Reset manual metadata to catalog values for \""+title+"\" (database record only)")
	resp, err := h.bookMetadataResponse(bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// UploadBookMetadataCover stores the operator's own artwork as an
// immutable local asset under the covers directory and returns its
// filename for the editor to reference in the override draft. The file
// is written ONCE (temp file + atomic rename) and never rewritten in
// place: a later upload is a NEW asset, and a queued job that named the
// previous file keeps reading it (pruning only removes assets no active
// job references, per pruneOrphanedOverrideCovers).
func (h *Handler) UploadBookMetadataCover(w http.ResponseWriter, r *http.Request) {
	bookID, ok := h.bookIDParam(w, r)
	if !ok {
		return
	}
	if h.svc.CoversDir == "" {
		writeError(w, http.StatusInternalServerError, "covers directory is not configured")
		return
	}
	// Bound the WHOLE request before multipart parsing: ParseMultipartForm's
	// memory cap limits in-memory storage only — an oversized file is spooled
	// to disk first, so the transport must be capped here (file limit + a
	// little headroom for the form framing) before the parser runs.
	r.Body = http.MaxBytesReader(w, r.Body, maxOverrideUploadBodyBytes)
	if err := r.ParseMultipartForm(maxOverrideCoverUploadBytes); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("cover upload exceeds the %d MB limit", maxOverrideCoverUploadBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field in upload: "+err.Error())
		return
	}
	defer file.Close()
	if header.Size > maxOverrideCoverUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("cover file exceeds the %d MB limit", maxOverrideCoverUploadBytes>>20))
		return
	}
	name := fmt.Sprintf("ovr-%d-%s.jpg", bookID, randomHex8())
	tmp, err := os.CreateTemp(h.svc.CoversDir, ".ovr-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create cover file: "+err.Error())
		return
	}
	tmpName := tmp.Name()
	wrote, copyErr := io.Copy(tmp, io.LimitReader(file, maxOverrideCoverUploadBytes+1))
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil && wrote > maxOverrideCoverUploadBytes {
		copyErr = fmt.Errorf("cover file exceeds the %d MB limit", maxOverrideCoverUploadBytes>>20)
	}
	if copyErr == nil && !isValidJPEG(tmpName) {
		copyErr = errors.New("the upload is not a JPEG image")
	}
	if copyErr != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusBadRequest, copyErr.Error())
		return
	}
	// The draft save that will reference this asset has not happened yet:
	// register it so a prune sweep (save/reset of the same book) running in
	// that window keeps the file (finding: pending upload vs prune). The
	// register AND the rename are one critical section under the per-book
	// cover lock — the same lock every prune holds — because the sweep
	// snapshots the pending set BEFORE its ReadDir: a register that landed
	// after the snapshot and a rename before the read would be an unkept,
	// visible file, deleted out from under a not-yet-saved draft.
	unlock := h.svc.ProcessingSvc.lockBookCover(bookID)
	h.svc.recordPendingCover(name)
	renameErr := func() error {
		err := os.Rename(tmpName, filepath.Join(h.svc.CoversDir, name))
		unlock()
		return err
	}()
	if renameErr != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "store cover file: "+renameErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"cover_file": name})
}

// isValidJPEG reports whether the file at path is a decodable JPEG image:
// the asset is served by <img> and staged into the engine as cover art,
// so a text file named .jpg must not enter the asset store.
func isValidJPEG(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, _, err = image.DecodeConfig(f)
	return err == nil
}

// overrideChangeSummary names the editor sections the saved draft
// touches, for the audit detail ("Edited manual metadata — title, year,
// cover"). An all-blank draft (a save that clears the row) reports the
// reset.
func overrideChangeSummary(o *MetadataOverride) string {
	var parts []string
	add := func(cond bool, name string) {
		if cond {
			parts = append(parts, name)
		}
	}
	add(o.Title != "", "title")
	add(o.Subtitle != "", "subtitle")
	add(o.Authors != nil, "authors")
	add(o.Narrators != nil, "narrators")
	add(o.SeriesName != "" || o.SeriesPos != "", "series")
	add(o.Description != "", "description")
	add(o.Genres != nil, "genres")
	add(o.Year != nil, "year")
	// The cover's state is reported once: the cover source line ("cover
	// (clear)", "cover (file)", ...) covers the cleared-"cover" entry too,
	// so a cleared cover is not named twice.
	coverCleared := o.CoverSource != ""
	if len(o.Cleared) > 0 {
		names := make([]string, 0, len(o.Cleared))
		for _, c := range o.Cleared {
			if coverCleared && c == "cover" {
				continue
			}
			names = append(names, c)
		}
		if len(names) > 0 {
			parts = append(parts, "cleared tags: "+strings.Join(names, ", "))
		}
	}
	if coverCleared {
		parts = append(parts, "cover ("+o.CoverSource+")")
	}
	add(o.Chapters != nil, "chapters")
	if len(parts) == 0 {
		return "all overrides cleared (record reset)"
	}
	return strings.Join(parts, ", ")
}

// persistedOverrideCoverFile reads the CURRENT row's override cover file (the
// asset the row references right now), or "" when the row is absent or does
// not reference a local asset. Prune callers derive their keep-set from THIS,
// read under the per-book lock, not from the caller's request draft: two
// concurrent saves can otherwise leave the row pointing at a NEW asset while
// a stale prune (built from an older draft) deletes it.
// ok reports whether the read succeeded: a transient query failure must NOT
// look like an empty keep-set, because the caller then prunes against no
// keep-set at all and can delete the asset the saved row references. The
// caller skips the sweep when ok is false.
func (h *Handler) persistedOverrideCoverFile(bookID int64) (coverFile string, ok bool) {
	var coverSource string
	err := h.svc.DB.QueryRow(
		"SELECT cover_source, cover_file FROM book_metadata_overrides WHERE book_id = ?", bookID,
	).Scan(&coverSource, &coverFile)
	switch {
	case err == nil:
		if coverSource != "file" {
			return "", true
		}
		return coverFile, true
	case errors.Is(err, sql.ErrNoRows):
		// No row: nothing to keep, safe to prune.
		return "", true
	default:
		// Transient DB failure: do not let a failed read masquerade as an
		// empty keep-set.
		return "", false
	}
}

// pendingCoverTTL bounds how long an uploaded override cover is kept by the
// pending-upload registry when the draft save never arrives (the file is
// small — capped at maxOverrideCoverUploadBytes — and the next prune sweep
// reaps expired entries).
const pendingCoverTTL = 24 * time.Hour

// recordPendingCover registers an uploaded override cover asset as
// in-use-by-an-unsaved-draft so prune sweeps keep it until the draft save
// persists the reference (or the entry expires).
// reapPendingCovers removes expired entries. The caller must hold
// pendingCoversMu; calling it from every registry access bounds growth even
// when uploads never reach a draft save.
func (s *Services) reapPendingCovers(now time.Time) {
	for name, exp := range s.pendingCovers {
		if !now.Before(exp) {
			delete(s.pendingCovers, name)
		}
	}
}

func (s *Services) recordPendingCover(name string) {
	s.pendingCoversMu.Lock()
	if s.pendingCovers == nil {
		s.pendingCovers = make(map[string]time.Time)
	}
	now := time.Now()
	s.reapPendingCovers(now)
	s.pendingCovers[name] = now.Add(pendingCoverTTL)
	s.pendingCoversMu.Unlock()
}

// pendingOverrideCoversFor returns this book's non-expired pending override
// cover assets (the prune keep-set contribution).
func (s *Services) pendingOverrideCoversFor(bookID int64) map[string]struct{} {
	s.pendingCoversMu.Lock()
	defer s.pendingCoversMu.Unlock()
	prefix := fmt.Sprintf("ovr-%d-", bookID)
	now := time.Now()
	s.reapPendingCovers(now)
	var out map[string]struct{}
	for name := range s.pendingCovers {
		if strings.HasPrefix(name, prefix) {
			if out == nil {
				out = make(map[string]struct{})
			}
			out[name] = struct{}{}
		}
	}
	return out
}

// dropPendingCoversFor forgets this book's pending cover entries (the draft
// save persisted the row, so the keep-set now comes from the row itself).
// Expired entries for other books are reaped on every registry access.
func (s *Services) dropPendingCoversFor(bookID int64) {
	s.pendingCoversMu.Lock()
	defer s.pendingCoversMu.Unlock()
	prefix := fmt.Sprintf("ovr-%d-", bookID)
	for name := range s.pendingCovers {
		if strings.HasPrefix(name, prefix) {
			delete(s.pendingCovers, name)
		}
	}
}

// pruneOrphanedOverrideCovers removes this book's override cover assets
// ("ovr-<bookID>-*.jpg") that neither the CURRENT override row (keepFile,
// read under the per-book lock by the caller) nor any active job's queued
// snapshot references. It mirrors pruneSupersededCovers' contract: an active
// job naming a file keeps it (a queued job's asset must outlive a later
// editor save, reset, or upload), and an unreadable reference scan aborts
// the sweep entirely rather than delete against an incomplete keep-set.
// The caller must hold the per-book lock for the WHOLE mutation+prune
// sequence (the mutation and this call together are the critical section)
// and pass the keep-set read from the persisted row under that same lock.
func (h *Handler) pruneOrphanedOverrideCovers(bookID int64, keepFile string) {
	if h.svc.CoversDir == "" {
		return
	}
	keep := map[string]struct{}{}
	if keepFile != "" {
		keep[keepFile] = struct{}{}
	}
	// Assets an upload wrote for a not-yet-saved draft: keep them (the draft
	// PUT may still reference them before the save persists the row).
	for name := range h.svc.pendingOverrideCoversFor(bookID) {
		keep[name] = struct{}{}
	}
	rows, err := h.svc.DB.Query(
		`SELECT m4b_merge_args FROM processing_jobs WHERE book_id = ? AND status IN ('queued','running')`, bookID,
	)
	if err != nil {
		return
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return
		}
		var args struct {
			Snapshot *snapshotJSON `json:"snapshot"`
		}
		if json.Unmarshal([]byte(raw), &args) == nil && args.Snapshot != nil {
			if cf := args.Snapshot.Snapshot.CoverFile; cf != "" && strings.HasPrefix(cf, "ovr-") {
				keep[cf] = struct{}{}
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return
	}
	// Housekeeping only: a covers-dir read failure leaves the files in
	// place.
	dirEntries, err := os.ReadDir(h.svc.CoversDir)
	if err != nil {
		return
	}
	// Prune ONLY this book's override assets: the covers directory is
	// SHARED (source covers, snapshot covers, other books' assets).
	prefix := fmt.Sprintf("ovr-%d-", bookID)
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if _, ok := keep[name]; ok {
			continue
		}
		os.Remove(filepath.Join(h.svc.CoversDir, name))
	}
}

// ── Validation ────────────────────────────────────────────────────────

const (
	maxOverrideTitleChars    = 500
	maxOverrideSubtitleChars = 500
	maxOverrideSeriesChars   = 200
	maxOverrideDescription   = 20000
	maxOverrideYearLow       = 1000
	maxOverrideYearHigh      = 9999
	maxOverridePeople        = 50
	maxOverridePersonName    = 200
	maxOverrideGenres        = 50
	maxOverrideGenreChars    = 100
	maxOverrideChapterTitle  = 500
	// maxOverrideChapterMillis bounds nonsense, not real audiobooks: one
	// year in milliseconds. The engine remains the final authority for
	// chapter timing at write time.
	maxOverrideChapterMillis = 365 * 24 * 3600 * 1000
)

// validateMetadataOverride checks the editor draft BEFORE it is stored:
// vocabulary, sizes, and shapes the engine would reject (blank chapter
// titles, non-positive times, out-of-order chapter ranges, a cover_file
// that is not this book's override asset, a non-public cover URL). This is
// the operator-facing, actionable gate; the engine re-validates at run time.
// ctx bounds the cover-URL host resolution (a stuck DNS lookup dies on the
// request clock, not the resolver's).
func validateMetadataOverride(ctx context.Context, o *MetadataOverride) error {
	if len(o.Title) > maxOverrideTitleChars {
		return fmt.Errorf("title is too long (max %d characters)", maxOverrideTitleChars)
	}
	if len(o.Subtitle) > maxOverrideSubtitleChars {
		return fmt.Errorf("subtitle is too long (max %d characters)", maxOverrideSubtitleChars)
	}
	if len(o.SeriesName) > maxOverrideSeriesChars || len(o.SeriesPos) > maxOverrideSeriesChars {
		return fmt.Errorf("series name/position is too long (max %d characters)", maxOverrideSeriesChars)
	}
	if len(o.Description) > maxOverrideDescription {
		return fmt.Errorf("description is too long (max %d characters)", maxOverrideDescription)
	}
	if o.Year != nil && (*o.Year < maxOverrideYearLow || *o.Year > maxOverrideYearHigh) {
		return fmt.Errorf("year must be between %d and %d (or cleared)", maxOverrideYearLow, maxOverrideYearHigh)
	}
	for name, list := range map[string][]string{"authors": o.Authors, "narrators": o.Narrators} {
		if list == nil {
			continue
		}
		if len(list) > maxOverridePeople {
			return fmt.Errorf("%s: too many entries (max %d)", name, maxOverridePeople)
		}
		for _, p := range list {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("%s: a person entry is empty", name)
			}
			if len(p) > maxOverridePersonName {
				return fmt.Errorf("%s: a name is too long (max %d characters)", name, maxOverridePersonName)
			}
		}
	}
	if o.Genres != nil {
		if len(o.Genres) > maxOverrideGenres {
			return fmt.Errorf("genres: too many entries (max %d)", maxOverrideGenres)
		}
		for _, g := range o.Genres {
			if len(g) > maxOverrideGenreChars {
				return fmt.Errorf("genres: an entry is too long (max %d characters)", maxOverrideGenreChars)
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range o.Cleared {
		if !validOverrideField(c) {
			return fmt.Errorf("cleared: unknown field %q (allowed: title, subtitle, authors, narrators, series, description, genres, year, cover, chapters)", c)
		}
		if seen[c] {
			return fmt.Errorf("cleared: field %q is listed twice", c)
		}
		seen[c] = true
	}
	switch o.CoverSource {
	case "":
		if o.CoverFile != "" || o.CoverURL != "" {
			return errors.New("cover_file/cover_url are set but cover_source is empty")
		}
	case "clear":
		if o.CoverFile != "" || o.CoverURL != "" {
			return errors.New("a cleared cover must not also reference a file or URL")
		}
	case "file":
		if o.CoverFile == "" {
			return errors.New("a file cover requires cover_file (upload it first)")
		}
		if !overrideCoverNameFor(o.CoverFile, o.BookID) {
			return fmt.Errorf("cover_file %q is not this book's override cover asset", o.CoverFile)
		}
		if o.CoverURL != "" {
			return errors.New("a file cover must not also reference a URL")
		}
	case "url":
		if o.CoverURL == "" {
			return errors.New("a URL cover requires cover_url")
		}
		u, err := url.Parse(o.CoverURL)
		// HTTPS only: the pinned engine fetches write_url covers with an
		// HTTPS-only transport, so an http:// URL would pass save-time
		// validation and only fail at run time (the job runs, then the
		// cover write fails). Accepted values must match the engine
		// contract, so the gate refuses non-HTTPS here.
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return fmt.Errorf("cover_url must be an absolute https URL: %q", o.CoverURL)
		}
		// The URL is queued as a write_url cover the ENGINE fetches at run
		// time, so it must pass the SAME public-destination guard the catalog
		// cover path uses (assertPublicCoverURL): private, loopback,
		// link-local, multicast, unspecified, and CGNAT addresses are
		// refused, and every resolved address is checked. Without this, an
		// operator could pin a URL like http://127.0.0.1/... and the engine
		// would fetch it — an SSRF hole the catalog path does not have
		// (plan: no SSRF exception for overrides).
		if _, err := assertPublicCoverURL(ctx, u); err != nil {
			return fmt.Errorf("cover_url: %w", err)
		}
		if o.CoverFile != "" {
			return errors.New("a URL cover must not also reference a file")
		}
	default:
		return fmt.Errorf("cover_source %q is not one of '', 'clear', 'file', 'url'", o.CoverSource)
	}
	if o.Chapters != nil {
		if len(o.Chapters) > maxOverrideChapters {
			return fmt.Errorf("chapters: too many entries (max %d)", maxOverrideChapters)
		}
		// prevEnd is the previous chapter's end (start+duration, uint64, so
		// no overflow at the per-chapter cap). A chapter that starts before
		// the previous one ends is overlapping or out of order: the UI
		// catches this too, but the server is the authoritative gate — a
		// direct PUT must not defer a determinable ordering error to a
		// processing run. The first chapter may start at 0 (a legal start;
		// the engine checks it against real playlength at write time).
		var prevEnd uint64
		for i, c := range o.Chapters {
			if strings.TrimSpace(c.Title) == "" {
				return fmt.Errorf("chapter %d has no title", i+1)
			}
			if len(c.Title) > maxOverrideChapterTitle {
				return fmt.Errorf("chapter %d title is too long (max %d characters)", i+1, maxOverrideChapterTitle)
			}
			if c.DurationMillis == 0 {
				return fmt.Errorf("chapter %d: duration is a positive integer in milliseconds", i+1)
			}
			if c.StartMillis > maxOverrideChapterMillis || c.DurationMillis > maxOverrideChapterMillis {
				return fmt.Errorf("chapter %d: start/duration are out of range", i+1)
			}
			if i > 0 && c.StartMillis < prevEnd {
				return fmt.Errorf("chapter %d starts at %d ms before the previous chapter ends at %d ms (ranges must be ordered and non-overlapping)", i+1, c.StartMillis, prevEnd)
			}
			prevEnd = c.StartMillis + c.DurationMillis
		}
	}
	return nil
}

// overrideCoverNameFor reports whether name is the override-asset naming
// scheme for THIS book ("ovr-<bookID>-<hex>.jpg"): the fixed prefix, this
// book's id, a non-empty lowercase-hex random part, and the fixed
// extension. A request may only reference its own book's assets — one
// book's upload is never another book's cover.
func overrideCoverNameFor(name string, bookID int64) bool {
	const prefix = "ovr-"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jpg") {
		return false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".jpg")
	want := strconv.FormatInt(bookID, 10) + "-"
	if !strings.HasPrefix(rest, want) {
		return false
	}
	hexPart := rest[len(want):]
	if hexPart == "" {
		return false
	}
	for _, c := range hexPart {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// overrideCoverName reports whether name is an override cover asset for
// ANY book (the /covers/* serving gate): the fixed prefix, digit book id,
// hex random part, fixed extension. Strictly validated so the static
// file handler never serves an unexpected path.
func overrideCoverName(name string) bool {
	const prefix = "ovr-"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jpg") {
		return false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".jpg")
	dash := strings.IndexByte(rest, '-')
	if dash <= 0 || dash == len(rest)-1 {
		return false
	}
	for _, c := range rest[:dash] {
		if c < '0' || c > '9' {
			return false
		}
	}
	for _, c := range rest[dash+1:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// randomHex8 returns 8 random hex characters for an override cover name:
// the uniqueness need is per-book collision avoidance (a handful of
// uploads per book), and crypto/rand is the tool without a dependency.
func randomHex8() string {
	var b [4]byte
	if _, err := randRead(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
