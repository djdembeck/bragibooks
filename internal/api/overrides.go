package api

import (
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

// maxOverrideCoverUploadBytes caps a manual cover upload: a cover is a
// thumbnail, not a book (same order as catalog cover fetches).
const maxOverrideCoverUploadBytes = 8 << 20

// maxOverrideChapters caps a manual chapter list: a legitimate audiobook
// has far fewer than this, and the cap bounds the JSON round trip and the
// engine's chapter write.
const maxOverrideChapters = 1000

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
	// override, '[]' = an explicit empty list).
	o.Authors = jsonStringsOrEmpty(authorsJSON)
	o.Narrators = jsonStringsOrEmpty(narratorsJSON)
	o.Genres = jsonStringsOrEmpty(genresJSON)
	o.Cleared = jsonStringsOrEmpty(clearedJSON)
	o.Chapters = jsonChaptersOrEmpty(chaptersJSON)
	return o, nil
}

// jsonStringsOrEmpty decodes the stored array convention: ” (no override)
// stays nil, any other content decodes to a slice ('[]' = explicit empty).
// A decode failure yields an empty slice (the fail-safe state: no value,
// no clear) — a corrupted row must not invent catalog content.
func jsonStringsOrEmpty(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

// jsonChaptersOrEmpty decodes the stored chapters convention (” = no
// override, any other content = the explicit list, '[]' = clear chapters).
func jsonChaptersOrEmpty(raw string) []SnapshotChapter {
	if raw == "" {
		return nil
	}
	var out []SnapshotChapter
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
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
	authors, _ := json.Marshal(nonNilStrings(o.Authors))
	narrators, _ := json.Marshal(nonNilStrings(o.Narrators))
	genres, _ := json.Marshal(nonNilStrings(o.Genres))
	cleared, _ := json.Marshal(nonNilStrings(o.Cleared))
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
		o.BookID, o.Title, o.Subtitle, string(authors), string(narrators),
		o.SeriesName, o.SeriesPos, o.Description, string(genres), year,
		string(cleared), o.CoverSource, o.CoverFile, o.CoverURL, chapters,
	)
	if err != nil {
		return fmt.Errorf("save metadata overrides: %w", err)
	}
	return nil
}

// nonNilStrings maps a nil slice to an empty one so JSON storage is '[]'
// (explicit empty) and never null.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
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
//   - Chapters: a non-nil list replaces the captured chapters and pins
//     the chapter policy to "write" — EXCEPT an empty list, which the
//     engine's job contract treats as an explicit chapter CLEAR.
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
		p := audio.WritePolicies{}
		if clearSet["title"] {
			eff.Title = ""
			p.Title = "clear"
		}
		if clearSet["subtitle"] {
			eff.Subtitle = ""
			p.Subtitle = "clear"
		}
		if clearSet["authors"] {
			eff.Authors = []string{}
			p.Authors = "clear"
		}
		if clearSet["narrators"] {
			eff.Narrators = []string{}
			p.Narrators = "clear"
		}
		if clearSet["series"] {
			eff.SeriesName = ""
			eff.SeriesPosition = ""
			p.Series = "clear"
		}
		if clearSet["description"] {
			eff.Description = ""
			p.Description = "clear"
		}
		if clearSet["genres"] {
			eff.Genres = []string{}
			p.Genres = "clear"
		}
		if clearSet["year"] {
			eff.Year = nil
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
	if o.Chapters != nil {
		if len(o.Chapters) == 0 {
			// An explicit empty list is the engine's chapter CLEAR (its
			// job contract: {"write":{"chapters":[]}} is an explicit
			// clear), not a write of zero chapters.
			eff.Chapters = nil
			pinned.Chapters = new("clear")
		} else {
			pinned.Chapters = new("write")
		}
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
		resp.EffectiveChapters = eff.Chapters
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
	var req bookMetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := validateMetadataOverride(o); err != nil {
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
	if err := h.saveMetadataOverride(o); err != nil {
		writeError(w, http.StatusInternalServerError, "save metadata overrides: "+err.Error())
		return
	}
	// When the cover reference changes, the previous override asset may
	// become unreferenced: prune it under the same per-book lock +
	// active-job check the capture prune uses, so a queued job's asset is
	// never removed.
	h.pruneOrphanedOverrideCovers(bookID, o)
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
	if err := h.deleteMetadataOverride(bookID); err != nil {
		writeError(w, http.StatusInternalServerError, "reset metadata: "+err.Error())
		return
	}
	// Guarantee the capture row's existence (the reset must not strand a
	// historical book without one) and prune override cover assets no job
	// references (the row's reference is gone).
	if snap, err := h.loadSnapshot(bookID); err != nil {
		writeError(w, http.StatusInternalServerError, "load snapshot: "+err.Error())
		return
	} else if snap == nil {
		if _, err := h.captureSnapshot(r.Context(), bookID, false); err != nil {
			writeError(w, http.StatusInternalServerError, "capture snapshot: "+err.Error())
			return
		}
	}
	h.pruneOrphanedOverrideCovers(bookID, nil)
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
	if err := r.ParseMultipartForm(maxOverrideCoverUploadBytes); err != nil {
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
	if err := os.Rename(tmpName, filepath.Join(h.svc.CoversDir, name)); err != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "store cover file: "+err.Error())
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

// pruneOrphanedOverrideCovers removes this book's override cover assets
// ("ovr-<bookID>-*.jpg") that neither the CURRENT override (newOv, nil
// after a reset) nor any active job's queued snapshot references. It
// mirrors pruneSupersededCovers' contract: an active job naming a file
// keeps it (a queued job's asset must outlive a later editor save,
// reset, or upload), and an unreadable reference scan aborts the sweep
// entirely rather than delete against an incomplete keep-set.
func (h *Handler) pruneOrphanedOverrideCovers(bookID int64, newOv *MetadataOverride) {
	if h.svc.CoversDir == "" {
		return
	}
	if h.svc.ProcessingSvc != nil {
		h.svc.ProcessingSvc.lockBookCover(bookID)()
	}
	keep := map[string]struct{}{}
	if newOv != nil && newOv.CoverSource == "file" && newOv.CoverFile != "" {
		keep[newOv.CoverFile] = struct{}{}
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
// titles, non-positive times, a cover_file that is not this book's
// override asset, a non-public cover URL). This is the operator-facing,
// actionable gate; the engine re-validates at run time.
func validateMetadataOverride(o *MetadataOverride) error {
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
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return fmt.Errorf("cover_url must be an absolute http(s) URL: %q", o.CoverURL)
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
