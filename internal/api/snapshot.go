package api

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/audiobookdb"
	"github.com/bragibooks/bragibooks/internal/models"
)

// This file is the authoritative-snapshot half of the processing cutover.
// A book's SELECTED catalog record (the release the operator chose at Match,
// plus the operator's own edits to the fields Bragi already edits) is
// captured ONCE into a durable per-book row, and that capture is what a job
// runs on. The engine never re-looks-up the catalog at worker time: the
// snapshot is the source of truth for naming, tags, chapters and artwork.
//
// The capture is re-taken on every Match save and every metadata edit, so
// the stored row always reflects the current selection. Historical rows
// that predate this table are resolved explicitly at queue time (or refused
// when they cannot be), never guessed.

// SnapshotChapter is the stored form of one chapter: integer-millisecond
// times, the shape the engine's chapters write policy consumes verbatim.
type SnapshotChapter struct {
	Title          string `json:"title"`
	StartMillis    uint64 `json:"start_millis"`
	DurationMillis uint64 `json:"duration_millis"`
}

// BookSnapshot is the durable per-book capture. Authors/Narrators are
// ORDERED arrays: the engine joins them with ", " and orders path segments
// by array position, so the operator's ordering is the output ordering
// (#479). ADBBookID is the canonical AudiobookDB book id that feeds the
// {book_id} token (#480), distinct from the legacy ASIN and the provider
// metadata_id.
type BookSnapshot struct {
	BookID       int64  `json:"book_id"`
	ADBBookID    string `json:"audiobookdb_book_id"`
	ADBReleaseID string `json:"audiobookdb_release_id"`
	Title        string `json:"title"`
	// Subtitle is the independently captured subtitle value: the selected
	// release's own subtitle, falling back to the book's, then the book's
	// disambiguation (the engine's documented precedence). It exists so the
	// title and subtitle write selections are backed by their own values —
	// a title-only write must preserve this component, not erase it (M1's
	// independent title/subtitle policies).
	Subtitle       string   `json:"subtitle"`
	Authors        []string `json:"authors"`
	Narrators      []string `json:"narrators"`
	SeriesName     string   `json:"series_name"`
	SeriesPosition string   `json:"series_position"`
	Description    string   `json:"description"`
	Genres         []string `json:"genres"`
	Year           *int     `json:"year"`
	// CoverFile is a filename under the covers directory: an immutable
	// local artwork reference. At run time it is staged into the job's
	// staging root (the engine's asset root) and embedded by local-file
	// cover policy, so the selected release's art never depends on a live
	// catalog fetch.
	CoverFile  string            `json:"cover_file"`
	Chapters   []SnapshotChapter `json:"chapters"`
	CapturedAt string            `json:"captured_at"`
}

// toSupplied converts a snapshot into the engine's input_supplied payload.
// The captured ADBBookID is forwarded as book_id so the {book_id} naming
// token (#480) resolves to the canonical AudiobookDB book id; the engine
// (>= 2.2.0) accepts a supplied book_id directly.
func (s *BookSnapshot) toSupplied() audio.SuppliedMetadata {
	// authors must serialize as an array (even when empty): the engine's
	// SuppliedInput.authors is a Vec<String> whose serde default accepts a
	// MISSING field, not JSON null, so an authorless book would fail job
	// parsing with null. make(..., 0, ...) yields a non-nil empty slice.
	meta := audio.SuppliedMetadata{
		Title:     s.Title,
		Authors:   append(make([]string, 0, len(s.Authors)), s.Authors...),
		Narrators: append([]string(nil), s.Narrators...),
		Genres:    append([]string(nil), s.Genres...),
	}
	if strings.TrimSpace(s.Subtitle) != "" {
		sub := strings.TrimSpace(s.Subtitle)
		meta.Subtitle = &sub
	}
	if s.SeriesName != "" {
		meta.SeriesName = &s.SeriesName
	}
	if s.SeriesPosition != "" {
		meta.SeriesPosition = &s.SeriesPosition
	}
	if s.Description != "" {
		meta.Description = &s.Description
	}
	if s.Year != nil {
		y := *s.Year
		meta.Year = &y
	}
	if s.ADBBookID != "" {
		meta.BookID = &s.ADBBookID
	}
	return meta
}

// WriteSelection is the per-field embedded-metadata write policy frozen into
// a job's queue-time snapshot (#518). Fields is the EXPLICIT selection the
// operator saved at queue time: nil (absent) is the default
// all-supported-available behavior, and a non-nil slice (possibly empty)
// writes exactly those fields and preserves the rest. The nil-vs-explicit
// distinction must survive the JSON round trip, so Fields has no omitempty.
// Cover and Chapters are the engine policy strings for the two non-scalar
// write paths: cover is "write" (embed the captured cover), "preserve"
// (keep the source's cover; single natively-copyable input only), "clear"
// (remove the cover — a per-book editor pin), or "write_url" (embed the
// operator's cover URL at run time — a per-book editor pin); chapters is
// "write" (embed the release's chapters + sidecar), "preserve" (skip all
// chapter mutation; single natively-copyable input only), or "clear"
// (remove the chapters — a per-book editor pin). A none-selected
// selection sets both to preserve: no embedded tag/cover/chapter mutation
// and no generated chapter sidecar, while naming still uses the effective
// metadata.
//
// ScalarPins are the per-book editor's EXPLICIT clears (B3), frozen onto
// the selection: the scalar fields named there run under the engine's
// "clear" policy on top of the selection-derived baseline, independent of
// the Fields choice. Nil (absent) = no pins; a non-nil map carries only
// the pinned fields.
type WriteSelection struct {
	Fields   []string `json:"fields"`
	Cover    string   `json:"cover"`
	Chapters string   `json:"chapters"`
	// ScalarPins freezes the editor's cleared scalar fields. JSON object
	// form (field -> "clear"): an empty-but-present object round-trips as
	// an empty map, and a missing key is nil — the distinction
	// "no pins" vs "pins were present" is load-bearing for a future
	// re-derivation, and the map never carries a value other than
	// "clear".
	ScalarPins map[string]string `json:"scalar_pins,omitempty"`
	// CoverURL freezes the editor's operator cover URL for the job's
	// write_url cover policy ("" when no URL cover is pinned): the run
	// embeds it at execution time, so the URL must live on the frozen
	// selection, not in the mutable override row.
	CoverURL string `json:"cover_url,omitempty"`
}

// snapshotJSON is the wire/storage form of a job's captured snapshot. It is
// embedded in the job args so a queued job is immutable: settings, profile
// edits, and re-matches after enqueue cannot change a captured job.
type snapshotJSON struct {
	Version  int                  `json:"snapshot_version"`
	Engine   audio.EngineIdentity `json:"engine"`
	Snapshot BookSnapshot         `json:"snapshot"`
	// Selection is the write policy the job runs under, frozen at queue time
	// from the operator's saved setting (or the default). A settings change
	// after enqueue cannot alter a queued job's writes.
	Selection *WriteSelection `json:"selection,omitempty"`
}

// snapshotRelevantChanged reports whether the update touched any field the
// snapshot persists: the catalog round trip (one GetBook + the release
// chapters + the cover) is then worth it. Pipeline-only PUTs (status,
// dest path, format, ...) do not change the snapshot's catalog content, so
// they skip the catalog hit entirely and upsert the row with the unchanged
// cover_file.
func snapshotRelevantChanged(req *UpdateBookRequest) bool {
	return req.Title != nil || req.ASIN != nil || req.Description != nil ||
		req.ReleaseDate != nil || req.Series != nil ||
		req.AudiobookdbBookID != nil || req.AudiobookdbReleaseID != nil ||
		req.Authors != nil || req.Narrators != nil || req.CoverImageURL != nil
}

// captureSnapshot re-captures the book's authoritative snapshot from the
// stored book row, its ordered people, and (when the book is catalog-linked
// and the update touched snapshot-relevant fields) the selected release's
// details and cover, reusing ONE GetBook for genres and images. It is called
// after a Match save or a metadata edit.
//
// Failure classes (the documented degradation):
//   - Catalog unavailable (GetBook/GetRelease/download): catalog-derived
//     fields RETAIN the previously captured values from the existing row,
//     local fields are refreshed, the save succeeds. A catalog that is down
//     must not block a metadata save, and a degraded row must not silently
//     lose previously captured chapters/genres/cover.
//   - DB read/write failure: the capture hard-fails. The caller then
//     INVALIDATES the row (deletes it) and reports 500: a book with no
//     snapshot row is refused at queue time with an explicit re-match and
//     re-preview message, never run on stale data.
func (h *Handler) captureSnapshot(ctx context.Context, bookID int64, changed bool) (catalogDegraded bool, err error) {
	// The per-book lock spans the WHOLE capture, starting with the reads:
	// the book row, the ordered people, and the existing snapshot are all
	// inputs to the row this capture writes, and a concurrent capture
	// writing between a read and the upsert would let a stale generation
	// land. It is the same lock QueueJob holds across its snapshot
	// read→insert, so a capture and an enqueue for one book never
	// interleave: a capture cannot prune the file a not-yet-inserted
	// enqueue already read (its active-job count is still zero in the
	// gap), and two captures cannot race so that one writes an
	// unreferenced file and the other prunes it before the first's upsert
	// references it. Holding it across the download (not just the
	// write+prune) is what closes the second race: the file a capture
	// writes is referenced by its own upsert before any other capture may
	// prune.
	if h.svc.ProcessingSvc != nil {
		defer h.svc.ProcessingSvc.lockBookCover(bookID)()
	}
	return h.captureSnapshotUnlocked(ctx, bookID, changed)
}

// captureSnapshotUnlocked is the capture core with the per-book lock
// ALREADY held by the caller (UpdateBook holds it across the book+people
// writes AND the capture, so an enqueue in that window cannot freeze the
// pre-update snapshot; a sync.Mutex is not re-entrant, so the locked
// wrapper above must not call this while holding the lock).
func (h *Handler) captureSnapshotUnlocked(ctx context.Context, bookID int64, changed bool) (catalogDegraded bool, err error) {
	var (
		title, description, series, releaseDate, coverURL string
		adbBook, adbRelease                               sql.NullString
	)
	if err := h.svc.DB.QueryRow(
		"SELECT title, description, series, release_date, audiobookdb_book_id, audiobookdb_release_id, COALESCE(cover_image_url, '') FROM books WHERE id = ?",
		bookID,
	).Scan(&title, &description, &series, &releaseDate, &adbBook, &adbRelease, &coverURL); err == sql.ErrNoRows {
		return false, fmt.Errorf("book %d not found", bookID)
	} else if err != nil {
		return false, fmt.Errorf("load book for snapshot: %w", err)
	}

	// Ordered by id (= insert/rowid order), which is the operator's
	// requested order from Match: the array position is the path/author
	// ordering (#479), so the read MUST be ordered, not rely on scan order.
	people, err := h.getOrderedPeopleByBookID(bookID)
	if err != nil {
		return false, fmt.Errorf("load people for snapshot: %w", err)
	}
	authors := orderedNames(filterPeople(people, "author"))
	narrators := orderedNames(filterPeople(people, "narrator"))

	snap := &BookSnapshot{
		BookID:       bookID,
		ADBBookID:    adbBook.String,
		ADBReleaseID: adbRelease.String,
		Title:        title,
		Description:  description,
		Authors:      authors,
		Narrators:    narrators,
		Year:         parseYear(releaseDate),
	}
	snap.SeriesName, snap.SeriesPosition = splitSeries(series)

	existing, err := h.loadSnapshot(bookID)
	if err != nil {
		return false, fmt.Errorf("load existing snapshot: %w", err)
	}
	// The row's catalog-derived content survives a degraded capture, but ONLY
	// when it belongs to the SAME catalog selection the new row names:
	//   - genres and cover are BOOK-level content: retain only when the ADB
	//     book id is unchanged (a re-match to a different book, or an unlink,
	//     must start with empty genres/cover, not the previous book's);
	//   - chapters are RELEASE-level content: retain only when BOTH the book
	//     id and the release id are unchanged (a changed release with an
	//     unavailable chapter detail must start empty, not the old release's).
	// existing is nil for a first-time capture (nothing to retain).
	if existing != nil {
		sameBook := existing.ADBBookID != "" && existing.ADBBookID == snap.ADBBookID
		if sameBook {
			snap.Genres, snap.CoverFile = existing.Genres, existing.CoverFile
		}
		if sameBook && existing.ADBReleaseID != "" && existing.ADBReleaseID == snap.ADBReleaseID {
			snap.Chapters = existing.Chapters
			// The subtitle is RELEASE-level content (the release's own value
			// wins in the precedence below): retain it under the same
			// unchanged-selection rule as chapters.
			snap.Subtitle = existing.Subtitle
		}
	}

	// Catalog enrichment for the SELECTED release: genres, chapters, and a
	// locally stored cover — one GetBook feeds genres AND the cover. It is
	// best-effort: a catalog that is down must not block a metadata save,
	// so on failure the row is refreshed with the new LOCAL fields while
	// retaining the previously captured catalog content (the values
	// pre-loaded above), and the degradation is reported to the caller.
	// A FIRST capture (existing == nil) always runs the full catalog round
	// trip, even with changed=false: the editor's first-time row creation
	// (save/reset on a historical book) and a match save both reach this
	// point without a prior row to retain, so the row this capture writes
	// IS the first — its catalog content cannot come from anywhere else.
	// Without the existing==nil term, a first capture ran the cover-only
	// backfill below and stored a row with genres=null, which every later
	// save then retained (a page that then crashed on the null join).
	if (changed || existing == nil) && snap.ADBBookID != "" {
		if book, err := h.getAudiobookDB().GetBook(ctx, snap.ADBBookID, "genres,images"); err == nil && book != nil {
			snap.Genres = genreTitles(book.Genres)
			if h.svc.CoversDir != "" {
				// The artwork the capture stores is the one Match selected
				// (the book's persisted cover_image_url, which Match fills
				// from the chosen release's artwork), NOT the first
				// book-level image: release artwork wins, and falling back
				// to book.Images[0] would capture a different cover than
				// the operator saw at Match.
				selectedURL := coverURL
				if selectedURL == "" && len(book.Images) > 0 {
					selectedURL = book.Images[0].URL
				}
				if file, ok := h.storeCoverForBook(ctx, bookID, snap, selectedURL); ok {
					snap.CoverFile = file
				}
			}
			// Subtitle precedence mirrors the engine's documented
			// resolution: the selected release's own subtitle when present,
			// then the book's, then the book's disambiguation. The PREVIOUS
			// release subtitle (retained above for an unchanged selection)
			// yields only to the RELEASE's OWN subtitle: a GetRelease
			// failure — or a successful release that carries none — keeps
			// the retained release-specific subtitle. Only a release without
			// its own subtitle AND nothing retained falls through to the
			// book-level fallbacks.
			if snap.ADBReleaseID != "" {
				if rel, err := h.getAudiobookDB().GetRelease(ctx, snap.ADBReleaseID, "chapterDetail"); err == nil && rel != nil {
					if rel.ChapterDetail != nil {
						if chs, ok := chaptersFromADB(rel.ChapterDetail.Chapters); ok {
							snap.Chapters = chs
						} else {
							// Chapter rows existed but every row carried
							// an invalid time: the detail is corrupt, not
							// chapterless. Keep the retained catalog
							// chapters — writing the empty list would
							// freeze an explicit clear for a release that
							// merely has bad data.
							log.Printf("snapshot %d: release chapter detail has no usable rows; keeping previously captured chapters", bookID)
						}
					}
					if own := strings.TrimSpace(rel.Subtitle); own != "" {
						snap.Subtitle = own
					}
				} else if err != nil {
					log.Printf("snapshot %d: release details unavailable: %v", bookID, err)
				}
			}
			if snap.Subtitle == "" {
				snap.Subtitle = subtitleFromCatalog(book, nil)
			}
		} else {
			log.Printf("snapshot %d: catalog unavailable, keeping previously captured catalog content: %v", bookID, err)
			catalogDegraded = true
		}
	} else if snap.ADBBookID != "" && snap.CoverFile == "" {
		// An EXISTING row with no cover: the catalog round trip above is
		// skipped for unchanged local-only saves (the metadata editor's
		// save and reset), but the run-time cover must not depend on it —
		// buildEngineJob would default to "clear" instead of embedding the
		// catalog artwork. Backfill the cover alone (no genres/chapters/
		// subtitle: the row exists, so those were retained from, or captured
		// by, an earlier full enrichment — a first capture took the branch
		// above). Best-effort, like the main path: a catalog failure keeps
		// the row coverless.
		if book, err := h.getAudiobookDB().GetBook(ctx, snap.ADBBookID, "genres,images"); err == nil && book != nil {
			if h.svc.CoversDir != "" {
				selectedURL := coverURL
				if selectedURL == "" && len(book.Images) > 0 {
					selectedURL = book.Images[0].URL
				}
				if file, ok := h.storeCoverForBook(ctx, bookID, snap, selectedURL); ok {
					snap.CoverFile = file
				}
			}
		} else if err != nil {
			log.Printf("snapshot %d: cover backfill: catalog unavailable: %v", bookID, err)
		}
	}

	// The snapshot write and the superseded-cover prune run under the per-book
	// lock acquired above (the whole capture critical section).
	if err := h.upsertSnapshot(snap); err != nil {
		return false, err
	}
	h.pruneSupersededCovers(bookID, snap)
	return catalogDegraded, nil
}

// pruneSupersededCovers deletes this book's cover files that are no longer
// referenced by its current snapshot NOR by any of its active (queued/running)
// jobs' queued snapshots. The active-job check is PER FILE, not a skip: a
// file some active job's snapshot names is kept; a file NO active job names
// (the pre-capture-specific legacy shared name, and older capture-specific
// images) is removed, so repeated captures do not accumulate orphan files.
//
// It must run under the per-book lock (acquired by captureSnapshot) so the
// reference read and the delete are atomic with the enqueue's snapshot
// read→insert: a job that already named a cover file can never see that file
// pruned, because by the time its row exists (status queued/running) the
// query above includes it.
func (h *Handler) pruneSupersededCovers(bookID int64, s *BookSnapshot) {
	// A capture with NO cover file prunes with an EMPTY current-reference
	// set: the previous capture's unreferenced covers are orphans (the
	// active-job references below still protect anything a queued/running
	// job names). An early return here leaked them: every capture that
	// lost its cover left the prior capture's file behind forever.
	keep := map[string]struct{}{}
	if s.CoverFile != "" {
		keep[s.CoverFile] = struct{}{}
	}
	// The set of cover filenames that must survive: the current snapshot's
	// reference plus every active job's queued snapshot reference. If the
	// active-reference scan itself fails, do NOT prune at all: deleting on an
	// incomplete keep-set would remove a file some active job names, so on a
	// query error the sweep is skipped (orphan covers simply accumulate).
	rows, err := h.svc.DB.Query(
		`SELECT m4b_merge_args FROM processing_jobs WHERE book_id = ? AND status IN ('queued','running')`, bookID,
	)
	if err != nil {
		return
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			// A row we cannot read may be an active job whose reference we
			// would miss from the keep-set: pruning against an incomplete
			// set can delete a file that job names, so abort (same as a
			// query error) rather than continue.
			rows.Close()
			return
		}
		var args struct {
			Snapshot *snapshotJSON `json:"snapshot"`
		}
		if json.Unmarshal([]byte(raw), &args) == nil && args.Snapshot != nil {
			if cf := args.Snapshot.Snapshot.CoverFile; cf != "" {
				keep[cf] = struct{}{}
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return
	}
	// Housekeeping only: a covers-dir read failure leaves the files in place.
	dirEntries, err := os.ReadDir(h.svc.CoversDir)
	if err != nil {
		return
	}
	// Prune ONLY this book's snapshot-cover files: the capture-specific
	// "<bookID>-<unixnano>.jpg" images and the legacy "<bookID>-snapshot.jpg".
	// The covers directory is SHARED: source covers are stored as
	// "<bookID>.jpg" (attachSourceCover) and other books' snapshot covers live
	// here too, so a whole-directory sweep would delete files it does not own.
	prefix := fmt.Sprintf("%d-", bookID)
	legacy := fmt.Sprintf("%d-snapshot.jpg", bookID)
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) && name != legacy {
			continue
		}
		if _, ok := keep[name]; ok {
			continue
		}
		os.Remove(filepath.Join(h.svc.CoversDir, name))
	}
}

// upsertSnapshot persists the capture row (insert-or-replace on book_id).
func (h *Handler) upsertSnapshot(s *BookSnapshot) error {
	authors, _ := json.Marshal(s.Authors)
	narrators, _ := json.Marshal(s.Narrators)
	genres, _ := json.Marshal(s.Genres)
	chapters, _ := json.Marshal(s.Chapters)
	var year sql.NullInt64
	if s.Year != nil {
		year = sql.NullInt64{Int64: int64(*s.Year), Valid: true}
	}
	_, err := h.svc.DB.Exec(
		`INSERT INTO book_snapshots (book_id, audiobookdb_book_id, audiobookdb_release_id, title,
	   subtitle, authors, narrators, series_name, series_position, description, genres, year, cover_file, chapters, captured_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	 ON CONFLICT(book_id) DO UPDATE SET
	   audiobookdb_book_id=excluded.audiobookdb_book_id,
	   audiobookdb_release_id=excluded.audiobookdb_release_id,
	   title=excluded.title,
	   subtitle=excluded.subtitle,
	   authors=excluded.authors, narrators=excluded.narrators,
	   series_name=excluded.series_name, series_position=excluded.series_position,
	   description=excluded.description, genres=excluded.genres, year=excluded.year,
	   cover_file=excluded.cover_file, chapters=excluded.chapters, captured_at=datetime('now')`,
		s.BookID, s.ADBBookID, s.ADBReleaseID, s.Title,
		s.Subtitle,
		string(authors), string(narrators), s.SeriesName, s.SeriesPosition,
		s.Description, string(genres), year, s.CoverFile, string(chapters),
	)
	if err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}
	return nil
}

// loadSnapshot reads the book's stored snapshot, or nil when no capture row
// exists (a historical book not yet matched through the new flow).
func (h *Handler) loadSnapshot(bookID int64) (*BookSnapshot, error) {
	return loadBookSnapshot(h.svc.DB, bookID)
}

// loadBookSnapshot is the shared database read for a book's authoritative
// capture, usable by both the API handler and the processing worker.
func loadBookSnapshot(db *sql.DB, bookID int64) (*BookSnapshot, error) {
	var (
		adbBook, adbRelease, title, subtitle, seriesName, seriesPosition, description string
		authorsJSON, narratorsJSON, genresJSON, chaptersJSON                          string
		coverFile                                                                     string
		year, bookIDCol                                                               sql.NullInt64
		capturedAt                                                                    sql.NullString
	)
	err := db.QueryRow(
		`SELECT book_id, audiobookdb_book_id, audiobookdb_release_id, title,
		        subtitle, authors, narrators, series_name, series_position, description, genres,
		        year, cover_file, chapters, captured_at
		 FROM book_snapshots WHERE book_id = ?`,
		bookID,
	).Scan(&bookIDCol, &adbBook, &adbRelease, &title, &subtitle, &authorsJSON,
		&narratorsJSON, &seriesName, &seriesPosition, &description, &genresJSON,
		&year, &coverFile, &chaptersJSON, &capturedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	s := &BookSnapshot{
		BookID: bookIDCol.Int64, ADBBookID: adbBook, ADBReleaseID: adbRelease,
		Title: title, Subtitle: subtitle, SeriesName: seriesName,
		SeriesPosition: seriesPosition, Description: description,
		CoverFile: coverFile, CapturedAt: capturedAt.String,
	}
	if year.Valid {
		v := int(year.Int64)
		s.Year = &v
	}
	// A stored "null" (a catalog book with NO genres, which the capture
	// writes for an empty list) unmarshals into a NIL slice without error;
	// normalize it to the empty slice so the snapshot's wire shape keeps
	// its string[] contract — a null genres payload crashed the metadata
	// editor for genreless books. A decode ERROR is corruption (like the
	// chapters arrays below), not a valid empty list: fail closed.
	if err := json.Unmarshal([]byte(authorsJSON), &s.Authors); err != nil {
		return nil, fmt.Errorf("decode authors: %w", err)
	}
	if s.Authors == nil {
		s.Authors = []string{}
	}
	if err := json.Unmarshal([]byte(narratorsJSON), &s.Narrators); err != nil {
		return nil, fmt.Errorf("decode narrators: %w", err)
	}
	if s.Narrators == nil {
		s.Narrators = []string{}
	}
	if err := json.Unmarshal([]byte(genresJSON), &s.Genres); err != nil {
		return nil, fmt.Errorf("decode genres: %w", err)
	}
	if s.Genres == nil {
		s.Genres = []string{}
	}
	// A MALFORMED chapters value is corruption, not a valid empty list: a
	// silent [] would read as the engine's explicit "clear" at run time and
	// wipe the source's chapters. Fail closed — the book must be re-matched
	// (or re-saved) before it can queue — rather than clearing chapters on
	// a corrupt row. A genuinely empty list ("[]") decodes to a non-nil
	// empty slice with no error and is left as the explicit clear.
	if chaptersJSON != "" {
		if err := json.Unmarshal([]byte(chaptersJSON), &s.Chapters); err != nil {
			return nil, fmt.Errorf("load snapshot: corrupt chapters column for book %d: %w", bookID, err)
		}
	}
	return s, nil
}

// validateWriteSelection checks a frozen queue-time write selection for the
// worker-side defense invariants: every field in the vocabulary, the
// cover/chapters policies in the Bragi-derived subset (write/preserve from
// the settings selection, plus the per-book editor's clear/write_url pins),
// and the scalar pins (only the editor's vocabulary, only "clear" values).
// A corrupted row must fail with a clear re-queue message, never run under a
// guessed policy.
func validateWriteSelection(sel *WriteSelection) error {
	if sel == nil {
		return nil // default behavior, nothing to validate
	}
	for _, f := range sel.Fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !slices.Contains(audio.SelectableFields, f) {
			return fmt.Errorf("stored write selection names unsupported field %q", f)
		}
	}
	if !slices.Contains([]string{
		audio.WritePolicyValueWrite, audio.WritePolicyValuePreserve,
		audio.WritePolicyValueClear, "write_url",
	}, sel.Cover) {
		return fmt.Errorf("stored cover write policy %q is not write/preserve/clear/write_url", sel.Cover)
	}
	if !slices.Contains([]string{
		audio.ChaptersPolicyValueWrite, audio.ChaptersPolicyValuePreserve,
		audio.WritePolicyValueClear,
	}, sel.Chapters) {
		return fmt.Errorf("stored chapter write policy %q is not write/preserve/clear", sel.Chapters)
	}
	for f, p := range sel.ScalarPins {
		if !validOverrideField(f) || f == "cover" || f == "chapters" {
			return fmt.Errorf("stored scalar pin names unsupported field %q", f)
		}
		if p != audio.WritePolicyValueClear {
			return fmt.Errorf("stored scalar pin %q carries policy %q (only clear is pinned)", f, p)
		}
	}
	return nil
}

// buildEngineJob assembles the structured engine job from a captured
// snapshot: the effective metadata becomes input_supplied, the destination
// and path format come from the resolved queue-time pair, and the immutable
// chapter/artwork references are staged for this run. The write policy is the
// job's frozen selection (args.Snapshot.Selection, #518): scalar fields come
// from it (nil selection = the default all-supported-available policy), and
// the cover/chapters engine policies follow its cover/chapters entries. The
// cover policy is the only per-run filesystem step: a write cover is staged
// into the output root (the engine's asset root) under a job-owned hidden
// name, then referenced by absolute path, because the engine requires a
// local cover file to live under the job's output root. A missing or
// unreadable cover degrades to "clear" (no cover) rather than failing the
// run: the selection's naming and chapters are the load-bearing parts, and
// artwork loss is reported on the snapshot capture, not hidden here.
// buildEngineJob returns the job file plus the absolute path of the cover
// Bragi staged into the output root for this run ("" when none was staged);
// the caller removes that file after RunJob in every case, since the engine
// has already copied the bytes into the output on success.
func buildEngineJob(ctx context.Context, args jobArgs, jobID string, snap BookSnapshot, outputRoot, pathFormat string) (audio.JobFile, string, error) {
	// The engine requires a local cover file under its asset root: create
	// the root up front so staging cannot write into a directory that only
	// the engine later creates (or never creates on an early failure).
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return audio.JobFile{}, "", fmt.Errorf("create output root %s: %w", outputRoot, err)
	}
	supplied := snap.toSupplied()
	// Production rows always carry a non-nil snapshot (the worker refuses
	// legacy rows before reaching here); a nil selection inside it — or a
	// nil snapshot in unit tests — is the default baseline.
	var sel *WriteSelection
	if args.Snapshot != nil {
		sel = args.Snapshot.Selection
	}
	// Normalize ONCE and derive the policies from the normalized list:
	// startup validation (ValidateWriteSelectionForEngine) trims field
	// names, so a stored value like ` title ` reaches here with padding.
	// WritePolicyFor compares literal names; feeding it the raw strings
	// would silently skip the recognized field's write.
	var normalizedFields []string
	if sel != nil && sel.Fields != nil {
		norm, err := audio.NormalizeSelectableFields(sel.Fields)
		if err != nil {
			return audio.JobFile{}, "", err
		}
		normalizedFields = norm
	}
	jf := audio.JobFile{
		SchemaVersion: audio.JobSchemaVersion,
		Inputs:        args.SrcPaths,
		InputSupplied: supplied,
		OutputRoot:    outputRoot,
		PathFormat:    audio.NormalizeEnginePathFormat(pathFormat),
	}
	// The frozen selection owns the per-field policy: nil (no explicit
	// selection saved) derives the all-supported-available default. A
	// selection with nil Fields (a non-default chapter policy saved
	// alongside the default scalar selection) keeps the scalar default and
	// applies only its non-scalar policies below.
	jf.Policies = audio.AllSupportedWritePolicyFor(supplied)
	if normalizedFields != nil {
		jf.Policies = audio.WritePolicyFor(normalizedFields, supplied)
	}
	// The per-book editor's EXPLICIT clears (B3) are authoritative on top
	// of the selection-derived baseline: a cleared field runs under the
	// engine's "clear" policy regardless of the selection (the operator
	// deliberately removed that tag; the selection checkbox never
	// re-enables it).
	if sel != nil {
		for f, p := range sel.ScalarPins {
			switch f {
			case "title":
				jf.Policies.Title = p
			case "subtitle":
				jf.Policies.Subtitle = p
			case "authors":
				jf.Policies.Authors = p
			case "narrators":
				jf.Policies.Narrators = p
			case "series":
				jf.Policies.Series = p
			case "description":
				jf.Policies.Description = p
			case "genres":
				jf.Policies.Genres = p
			case "year":
				jf.Policies.Year = p
			}
		}
	}

	// Cover: the frozen selection's cover policy picks the engine policy.
	// write (the default behavior) stages the captured file when one exists
	// (for an operator cover edit, snap.CoverFile IS the override's asset —
	// applyMetadataOverride already layered it in), degrading to clear when
	// it does not; preserve skips all cover mutation (single
	// natively-copyable input only — the engine rejects it with an explicit
	// diagnosed error on a merge); clear removes the cover (the per-book
	// editor's pin); write_url embeds the operator's cover URL at run time
	// (the per-book editor's pin — the engine's own remote-embed path, the
	// same surface catalog covers use). The policy is honored whenever a
	// selection is present, including a pin-forced one with nil Fields.
	var stagedCover string
	coverPolicy := audio.WritePolicyValueWrite
	if sel != nil {
		coverPolicy = sel.Cover
	}
	switch coverPolicy {
	case audio.WritePolicyValuePreserve:
		jf.Cover = json.RawMessage(`"preserve"`)
	case "write_url":
		// A URL the ENGINE fetches at run time is not a URL Bragi can
		// vouch for: long after the save-time guard, the engine re-resolves
		// the host over live DNS, and a name that passed then can rebind to
		// a private address by then. Bragi therefore never hands the engine
		// the URL: it fetches the cover through the pinned downloader
		// (public-HTTPS guard re-asserted on EVERY redirect hop, dialing
		// only the validated address) into a staged file under the output
		// root, and freezes the LOCAL file as a write cover — the same
		// surface snapshot covers use. A URL that no longer passes (or a
		// fetch that fails) degrades to "clear" with a log, the same way an
		// unreadable local cover degrades: the selection's naming and
		// chapters are the load-bearing parts. A missing URL (a pin with no
		// value, which save-time validation prevents) degrades to the bare
		// policy, which in supplied mode embeds nothing.
		if sel != nil && sel.CoverURL != "" {
			dest := filepath.Join(outputRoot, fmt.Sprintf(".bragi-cover-url-%d-%s.jpg", snap.BookID, jobID))
			dlCtx, dlCancel := context.WithTimeout(ctx, coverDownloadTimeout)
			derr := downloadCoverFile(dlCtx, sel.CoverURL, dest)
			dlCancel()
			if derr == nil {
				if raw, merr := json.Marshal(map[string]string{"write": dest}); merr == nil {
					jf.Cover = raw
					stagedCover = dest
				} else {
					log.Printf("encode cover policy for book %d: %v", snap.BookID, merr)
				}
			} else {
				log.Printf("book %d: run-time cover download failed for %s: %v (degrading to no cover)", snap.BookID, redactCoverURL(sel.CoverURL), derr)
				jf.Cover = json.RawMessage(`"clear"`)
			}
		} else {
			jf.Cover = json.RawMessage(`"write_url"`)
		}
	default:
		jf.Cover = json.RawMessage(`"clear"`)
		if snap.CoverFile != "" {
			if staged, cerr := stageCoverUnderRoot(snap.CoverFile, outputRoot, jobID, snap.BookID); cerr == nil && staged != "" {
				// The engine's object form for a local cover file.
				if raw, merr := json.Marshal(map[string]string{"write": staged}); merr == nil {
					jf.Cover = raw
				} else {
					log.Printf("encode cover policy for book %d: %v", snap.BookID, merr)
				}
				stagedCover = staged
			} else if cerr != nil {
				// No usable local cover: embed nothing rather than fetch at
				// run time (supplied mode is offline) or fail.
				log.Printf("stage cover %s for book %d: %v", snap.CoverFile, snap.BookID, cerr)
			}
		}
	}

	// Chapters: the frozen selection's chapter policy picks the engine
	// policy. write embeds the selected release's explicit chapter list when
	// the snapshot captured one (immutable chapter references — the selected
	// edition's chapters, not a re-derivation from input tracks); an empty
	// list is an explicit clear. preserve skips all chapter mutation — no
	// embedding, no sidecar (single natively-copyable input only, enforced
	// by the engine with a diagnosed error otherwise).
	// Chapters honor the frozen policy whenever a selection is present —
	// including the chapter-policy-only shape (nil Fields, non-default
	// chapters), which is exactly the independent-setting case.
	chapterPolicy := audio.ChaptersPolicyValueWrite
	if sel != nil {
		chapterPolicy = sel.Chapters
	}
	switch {
	case chapterPolicy == audio.ChaptersPolicyValuePreserve:
		jf.Chapters = json.RawMessage(`"preserve"`)
	case chapterPolicy == audio.WritePolicyValueClear:
		// The per-book editor's chapter clear pin: remove the chapters
		// (the engine's explicit-clear shape, same as an empty write list).
		jf.Chapters = json.RawMessage(`{"write":{"chapters":[]}}`)
	case len(snap.Chapters) > 0:
		list := make([]audio.JobChapter, 0, len(snap.Chapters))
		for _, c := range snap.Chapters {
			list = append(list, audio.JobChapter{
				Title: c.Title, StartMillis: c.StartMillis, DurationMillis: c.DurationMillis,
			})
		}
		enc, err := json.Marshal(list)
		if err != nil {
			return jf, stagedCover, fmt.Errorf("encode chapters: %w", err)
		}
		jf.Chapters = json.RawMessage(`{"write":{"chapters":` + string(enc) + `}}`)
	case snap.Chapters != nil:
		jf.Chapters = json.RawMessage(`{"write":{"chapters":[]}}`)
	default:
		jf.Chapters = nil
	}

	return jf, stagedCover, nil
}

// coversDirPath is the app's on-disk covers directory, set once at startup
// (it is derived from the database path and cannot change at runtime). The
// worker stages a book's captured cover from here into the engine's asset
// root at run time.
var coversDirPath string

// SetCoversDir records the covers directory for the worker's cover staging.
func SetCoversDir(dir string) { coversDirPath = dir }

// stageCoverUnderRoot copies the book's captured cover (by filename in the
// covers directory) into the output root under a job-owned hidden name and
// returns its absolute path. The name carries the job ID so two runs of the
// same book can never collide on the same staged file: the O_EXCL create is
// the race detector, and a collision means the file already belongs to
// another run's staging, not this one.
func stageCoverUnderRoot(coverFile, outputRoot, jobID string, bookID int64) (string, error) {
	if coversDirPath == "" {
		return "", errors.New("covers directory is not configured")
	}
	src := filepath.Join(coversDirPath, filepath.Base(coverFile))
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("cover file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("cover file is not a regular file")
	}
	dest := filepath.Join(outputRoot, fmt.Sprintf(".bragi-cover-%d-%s%s", bookID, jobID, filepath.Ext(coverFile)))
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return "", err
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		os.Remove(dest)
		return "", err
	}
	return abs, nil
}

// orderedNames returns the people's names in their stored (insert) order.
// The people table's rowid IS the operator's ordering because Match writes
// them in request order; preserving that order is what makes #479's
// singular/plural people deterministic.
func orderedNames(people []models.Person) []string {
	out := make([]string, 0, len(people))
	for _, p := range people {
		if strings.TrimSpace(p.Name) != "" {
			out = append(out, strings.TrimSpace(p.Name))
		}
	}
	return out
}

// splitSeries parses the books.series display string "Name #1" into its
// name and position parts. A name without a trailing "#N" yields just the
// name; a leading "#N" with no name is treated as an empty name.
func splitSeries(series string) (name, position string) {
	series = strings.TrimSpace(series)
	if series == "" {
		return "", ""
	}
	if i := strings.LastIndex(series, " #"); i >= 0 {
		return strings.TrimSpace(series[:i]), strings.TrimSpace(series[i+2:])
	}
	return series, ""
}

// parseYear extracts a four-digit year from a date string (YYYY, YYYY-MM-DD,
// or a leading run of digits), or nil when none is present.
func parseYear(date string) *int {
	s := strings.TrimSpace(date)
	if len(s) < 4 {
		return nil
	}
	digits := s
	if i := strings.IndexAny(digits, "-/"); i > 0 {
		digits = digits[:i]
	}
	if len(digits) < 4 {
		return nil
	}
	n := 0
	for _, c := range digits[:4] {
		if c < '0' || c > '9' {
			return nil
		}
		n = n*10 + int(c-'0')
	}
	if n < 1000 || n > 9999 {
		return nil
	}
	return &n
}

// subtitleFromCatalog resolves the subtitle the snapshot captures, in the
// engine's documented precedence: the selected release's own subtitle when
// present, then the book's, then the book's disambiguation. A nil release
// (no release selected, or its detail fetch failed) applies the book-level
// fallbacks only.
func subtitleFromCatalog(book *audiobookdb.Book, rel *audiobookdb.Release) string {
	if rel != nil && strings.TrimSpace(rel.Subtitle) != "" {
		return strings.TrimSpace(rel.Subtitle)
	}
	if book != nil && book.Subtitle != nil && strings.TrimSpace(*book.Subtitle) != "" {
		return strings.TrimSpace(*book.Subtitle)
	}
	if book != nil && book.Disambiguation != nil && strings.TrimSpace(*book.Disambiguation) != "" {
		return strings.TrimSpace(*book.Disambiguation)
	}
	return ""
}

// genreTitles flattens ADB genre references to their display titles.
func genreTitles(genres []audiobookdb.IdTitle) []string {
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		if t := strings.TrimSpace(g.Title); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// chaptersFromADB converts ADB chapter details to the stored
// integer-millisecond form the engine consumes. A NEGATIVE offset or
// duration (a catalog row with signed-int fields carrying a bad value) is
// discarded, not converted: a negative int cast to uint64 wraps to a huge
// value, which the engine would treat as a multi-year chapter. A chapter
// whose times are both non-negative is kept; one with a negative start or
// duration is dropped so a single bad row cannot poison the whole capture
// with a wrapped timestamp.
//
// The boolean distinguishes a real chapter list from UNAVAILABLE one: rows
// existed but every row was invalid means the catalog data is corrupt, not
// that the release has no chapters. An empty-but-valid detail (no rows) is
// a genuine empty list (ok=true, len 0); rows-existed-but-all-invalid
// reports ok=false so the caller keeps the previously captured chapters
// instead of freezing an explicit clear.
func chaptersFromADB(chs []audiobookdb.Chapter) ([]SnapshotChapter, bool) {
	out := make([]SnapshotChapter, 0, len(chs))
	for _, c := range chs {
		if c.StartOffsetMs < 0 || c.LengthMs < 0 {
			continue
		}
		out = append(out, SnapshotChapter{
			Title:          c.Title,
			StartMillis:    uint64(c.StartOffsetMs),
			DurationMillis: uint64(c.LengthMs),
		})
	}
	return out, len(out) > 0 || len(chs) == 0
}

// storeCoverForBook downloads the selected book's primary cover (already
// fetched by the caller, reusing one GetBook) into the covers directory
// under a CAPTURE-SPECIFIC immutable filename and returns it. It returns
// false (and logs) when there is no cover or the download fails; a capture
// must never hard-fail on artwork.
//
// The name is unique per capture (<bookID>-<unixnano>.jpg): a later capture
// — success or failure — can never overwrite or delete the file the book's
// active (queued/running) job's snapshot references, which a shared
// "<bookID>-snapshot.jpg" name did (finding: a failed later download
// removed the file an earlier queued job's snapshot named). Failed
// captures only ever remove their own new, not-yet-referenced file.
// Covers superseded by a new capture are pruned opportunistically in
// upsertSnapshot (only files no queued/running job references).
func (h *Handler) storeCoverForBook(ctx context.Context, bookID int64, s *BookSnapshot, coverURL string) (string, bool) {
	if coverURL == "" {
		return "", false
	}
	name := fmt.Sprintf("%d-%d.jpg", bookID, time.Now().UnixNano())
	dest := filepath.Join(h.svc.CoversDir, name)
	dlCtx, cancel := context.WithTimeout(ctx, coverDownloadTimeout)
	defer cancel()
	if err := downloadCoverFile(dlCtx, coverURL, dest); err != nil {
		log.Printf("snapshot %d: cover download: %v", bookID, err)
		os.Remove(dest) // only our own new file, never one a snapshot references
		return "", false
	}
	return name, true
}

// coverDownloadTimeout bounds a snapshot cover fetch so a slow catalog does
// not stall a save.
const coverDownloadTimeout = 15 * time.Second

// coverMaxBytes caps a snapshot cover fetch at the PINNED engine's
// contract (m4b-merge v2.2.0 rejects local cover art over 5 MiB), so a
// fetched catalog cover that the engine would refuse is never staged. The
// upload path shares the same limit (maxOverrideCoverUploadBytes); a cover
// is a thumbnail, not a book, on both.
const coverMaxBytes = 5 << 20

// coverHop is one public, IP-pinned step of a cover fetch: the host was
// resolved, every address was checked as public, and the connection goes
// only to those addresses. Pinning the dial to the validated answers closes
// DNS-rebinding between the check and the connect: a name whose record
// flips to a private address after validation cannot reach it, and the
// original hostname is kept for Host/Authority and TLS (SNI, cert check).
type coverHop struct {
	url  *url.URL
	ip   string
	port string
}

// lookupCoverHost resolves host under the capture's context deadline, so a
// stuck DNS resolution dies on the same clock as the HTTP fetch instead of
// holding the metadata-save request past the intended cover timeout.
// Var-bound so tests may substitute a deterministic resolver (the override
// save-time URL guard resolves the host too, and a test must not depend on
// a live resolver answering for its fixture domain).
var lookupCoverHost = func(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// assertPublicCoverURL refuses a cover destination that is not a public
// HTTPS URL: private, loopback, link-local, multicast, unspecified, and
// carrier-grade NAT ranges are not public destinations, and a
// catalog-provided (or redirect-hopped) cover URL must never fetch from
// one. The scheme must be HTTPS, not merely http(s): the pinned engine
// (m4b-merge v2.2.0) fetches write_url covers itself and its contract is
// HTTPS-only, so an http:// URL that passes save-time validation would be
// frozen into a job and only fail at run time. The host is resolved and
// EVERY address checked, because a name resolving to mixed records must not
// pass on the public one. Returns the pinned hop (first validated address)
// the fetch must connect to.
func assertPublicCoverURL(ctx context.Context, u *url.URL) (coverHop, error) {
	if u == nil {
		return coverHop{}, errors.New("empty cover URL")
	}
	if u.Scheme != "https" {
		return coverHop{}, fmt.Errorf("cover URL scheme %q is not https (the engine fetches covers over HTTPS only)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return coverHop{}, errors.New("cover URL has no host")
	}
	// Bound the lookup to the capture deadline: a stuck DNS resolution must
	// die on the same clock as the HTTP fetch, not hold the metadata-save
	// request past the intended cover timeout (finding: DNS not on deadline).
	addrs, err := lookupCoverHost(ctx, host)
	if err != nil {
		return coverHop{}, fmt.Errorf("resolve cover host: %w", err)
	}
	if len(addrs) == 0 {
		return coverHop{}, fmt.Errorf("cover host %s has no addresses", host)
	}
	pinned := ""
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			return coverHop{}, fmt.Errorf("cover host %s resolves to an unparsable address %s", host, a)
		}
		if isNonPublicCoverAddress(ip) {
			return coverHop{}, fmt.Errorf("cover host %s resolves to a non-public address", host)
		}
		if pinned == "" {
			pinned = ip.String()
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return coverHop{url: u, ip: pinned, port: port}, nil
}

// isNonPublicCoverAddress classifies an address as a non-public fetch
// destination: the standard private/loopback/link-local/multicast sets,
// unspecified (0.0.0.0, :: — locally routable on applicable systems), the
// 100.64.0.0/10 shared address space, and the remaining IANA
// special-purpose ranges (RFC 6890/1122/4291: documentation, benchmarking,
// Teredo, NAT64/DNS64 well-known, IPv4-mapped, 6to4), none of which a
// cover fetch may reach.
func isNonPublicCoverAddress(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4InCGNAT(ip) {
		return true
	}
	v4 := ip.To4()
	if v4 != nil {
		switch {
		// 192.0.0.0/24 IETF protocol assignments; 198.18.0.0/15 benchmarking;
		// 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 documentation (RFC 5737).
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0,
			v4[0] == 198 && (v4[1] == 18 || v4[1] == 19),
			v4[0] == 192 && v4[1] == 0 && v4[2] == 2,
			v4[0] == 198 && v4[1] == 51 && v4[2] == 100,
			v4[0] == 203 && v4[1] == 0 && v4[2] == 113:
			return true
		// 255.255.255.255/32 limited broadcast.
		case v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255:
			return true
		}
		return false
	}
	// IPv6 special-purpose: 2001:db8::/32 documentation, fc00::/7 unique-local
	// (IsPrivate covers fc00::/7 already, kept explicit for parity with the
	// IANA list), fe80::/10 link-local (IsLinkLocalUnicast), 64:ff9b::/96
	// NAT64/DNS64 well-known, ::ffff:0:0/96 IPv4-mapped (the mapped IPv4 is
	// re-checked through the v4 table above).
	if ip6 := ip.To16(); ip6 != nil {
		if ip6[0] == 0x20 && ip6[1] == 0x01 && ip6[2] == 0x0d && ip6[3] == 0xb8 {
			return true
		}
		// 2002::/16 6to4 (RFC 3056): the embedded IPv4 may be private, so
		// the address is not public-Internet-reachable in the general
		// sense and is rejected like the other special-purpose ranges.
		if ip6[0] == 0x20 && ip6[1] == 0x02 {
			return true
		}
		if ip6[0] == 0x64 && ip6[1] == 0xff && ip6[2] == 0x9b {
			return true
		}
		if ip6[0] == 0 && ip6[1] == 0 && ip6[2] == 0 && ip6[3] == 0 &&
			ip6[4] == 0 && ip6[5] == 0 && ip6[6] == 0 && ip6[7] == 0 &&
			ip6[8] == 0 && ip6[9] == 0 && ip6[10] == 0xff {
			return isNonPublicCoverAddress(net.IP(ip6[12:16]))
		}
	}
	return false
}

func ip4InCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// redactCoverURL keeps only the host of a cover URL for logs and wrapped
// errors: the operator-supplied path and query can carry credentials, and
// the host is what an operator needs to identify the failing destination.
func redactCoverURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<unparseable cover URL>"
	}
	return u.Scheme + "://" + u.Host
}

// downloadCoverFile fetches url into dest with the bounded client, writing
// to a temp file in the same directory and renaming into place so a partial
// download never leaves a truncated cover served.
func downloadCoverFile(ctx context.Context, coverURL string, dest string) error {
	parsed, err := url.Parse(coverURL)
	if err != nil {
		return fmt.Errorf("parse cover URL: %w", err)
	}
	if _, err := assertPublicCoverURL(ctx, parsed); err != nil {
		return fmt.Errorf("fetch cover %s: %w", redactCoverURL(coverURL), err)
	}
	current := parsed
	for hop := 0; hop < 4; hop++ {
		hp, err := assertPublicCoverURL(ctx, current)
		if err != nil {
			return fmt.Errorf("fetch cover %s: %w", redactCoverURL(coverURL), err)
		}
		resp, err := doPinnedCoverGet(ctx, hp)
		if err != nil {
			return fmt.Errorf("fetch cover %s: %w", redactCoverURL(coverURL), err)
		}
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			resp.Body.Close()
			loc, lerr := resp.Location()
			if lerr != nil || loc == nil {
				return fmt.Errorf("cover redirect %d has no location", resp.StatusCode)
			}
			current = current.ResolveReference(loc)
			continue
		case resp.StatusCode != http.StatusOK:
			resp.Body.Close()
			return fmt.Errorf("cover status %d", resp.StatusCode)
		}
		return writeCoverBody(ctx, resp, dest)
	}
	return errors.New("cover: too many redirects")
}

// doPinnedCoverGet fetches one hop, connecting only to the validated IP
// while keeping the hostname for routing, Host, and TLS.
func doPinnedCoverGet(ctx context.Context, hop coverHop) (*http.Response, error) {
	scheme := hop.url.Scheme
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, net.JoinHostPort(hop.ip, hop.port))
		},
		TLSClientConfig:       &tls.Config{ServerName: hop.url.Hostname()},
		ForceAttemptHTTP2:     scheme == "https",
		IdleConnTimeout:       5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	// Redirects are NOT followed by the client: the manual hop loop in
	// downloadCoverFile must see every 3xx so each destination is resolved,
	// validated as public, and pinned to its own addresses. A client-side
	// follow would dial the redirect target over this hop's pinned IP and
	// skip that destination's validation entirely.
	client := &http.Client{
		Timeout:       coverDownloadTimeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer transport.CloseIdleConnections()
	target := scheme + "://" + net.JoinHostPort(hop.url.Hostname(), hop.port)
	if p := hop.url.RequestURI(); p != "" {
		target += p
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Host = hop.url.Host
	resp, err := client.Do(req)
	if err != nil {
		// The transport error embeds the full request URL (path + query)
		// in its string; the caller re-wraps and logs it, so the URL must
		// not survive to the log line. Replace it with the redacted
		// hostname-only form (same destination, no credentials in path).
		return nil, fmt.Errorf("%s: %s", redactCoverURL(target), sanitizeTransportError(err, target))
	}
	return resp, nil
}

// sanitizeTransportError renders a transport error for a log line: a
// *url.Error carries the full request URL in its message, which would
// re-expose the operator's path+query past the caller's redaction. Strip
// the URL from the nested message; every other error passes through.
func sanitizeTransportError(err error, target string) string {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err.Error()
	}
	msg := urlErr.Err.Error()
	if i := strings.Index(msg, target); i >= 0 {
		msg = msg[:i] + redactCoverURL(target) + msg[i+len(target):]
	}
	return msg
}

// writeCoverBody streams the response into dest atomically, capped. The
// body is staged in an exclusively created, unpredictable temp file in
// dest's directory (os.CreateTemp is O_EXCL, 0o600) and renamed into
// place: a predictably named dest+".tmp" with O_TRUNC would let an
// output-dir writer plant the name as a symlink before the write and
// redirect it to an arbitrary path.
func writeCoverBody(ctx context.Context, resp *http.Response, dest string) error {
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".bragi-cover-url-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, coverMaxBytes+1))
	if cl := tmp.Close(); err == nil {
		err = cl
	}
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	if n > coverMaxBytes {
		os.Remove(tmpPath)
		return fmt.Errorf("cover exceeds %d bytes", coverMaxBytes)
	}
	return os.Rename(tmpPath, dest)
}

// getOrderedPeopleByBookID is the snapshot's people read: id-ordered so the
// result is the operator's requested order, deterministically.
func (h *Handler) getOrderedPeopleByBookID(bookID int64) ([]models.Person, error) {
	rows, err := h.svc.DB.Query(
		"SELECT id, book_id, name, role, audiobookdb_person_id, created_at FROM people WHERE book_id = ? ORDER BY id",
		bookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var people []models.Person
	for rows.Next() {
		var p models.Person
		var personID sql.NullString
		if err := rows.Scan(&p.ID, &p.BookID, &p.Name, &p.Role, &personID, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.AudiobookdbPersonID = personID
		people = append(people, p)
	}
	return people, rows.Err()
}
