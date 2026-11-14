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
	BookID         int64    `json:"book_id"`
	ADBBookID      string   `json:"audiobookdb_book_id"`
	ADBReleaseID   string   `json:"audiobookdb_release_id"`
	Title          string   `json:"title"`
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

// snapshotJSON is the wire/storage form of a job's captured snapshot. It is
// embedded in the job args so a queued job is immutable: settings, profile
// edits, and re-matches after enqueue cannot change a captured job.
type snapshotJSON struct {
	Version  int                  `json:"snapshot_version"`
	Engine   audio.EngineIdentity `json:"engine"`
	Snapshot BookSnapshot         `json:"snapshot"`
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
	var (
		title, description, series, releaseDate string
		adbBook, adbRelease                     sql.NullString
	)
	if err := h.svc.DB.QueryRow(
		"SELECT title, description, series, release_date, audiobookdb_book_id, audiobookdb_release_id FROM books WHERE id = ?",
		bookID,
	).Scan(&title, &description, &series, &releaseDate, &adbBook, &adbRelease); err == sql.ErrNoRows {
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
	// The row's catalog-derived content survives a degraded capture: it is
	// only replaced by fresh catalog values when the catalog is reachable.
	// existing is nil for a first-time capture (nothing to retain).
	if existing != nil {
		snap.Genres, snap.Chapters, snap.CoverFile = existing.Genres, existing.Chapters, existing.CoverFile
	}

	// The per-book lock spans the whole capture critical section: the cover
	// download, the snapshot WRITE, and the superseded-cover PRUNE (defer
	// releases it at function end). It is the same lock QueueJob holds across
	// its snapshot read→insert, so a capture and an enqueue for one book never
	// interleave: a capture cannot prune the file a not-yet-inserted enqueue
	// already read (its active-job count is still zero in the gap), and two
	// captures cannot race so that one writes an unreferenced file and the
	// other prunes it before the first's upsert references it. Holding it
	// across the download (not just the write+prune) is what closes the second
	// race: the file a capture writes is referenced by its own upsert before
	// any other capture may prune.
	if h.svc.ProcessingSvc != nil {
		defer h.svc.ProcessingSvc.lockBookCover(bookID)()
	}

	// Catalog enrichment for the SELECTED release: genres, chapters, and a
	// locally stored cover — one GetBook feeds genres AND the cover. It is
	// best-effort: a catalog that is down must not block a metadata save,
	// so on failure the row is refreshed with the new LOCAL fields while
	// retaining the previously captured catalog content (the values
	// pre-loaded above), and the degradation is reported to the caller.
	if changed && snap.ADBBookID != "" {
		if book, err := h.getAudiobookDB().GetBook(ctx, snap.ADBBookID, "genres,images"); err == nil && book != nil {
			snap.Genres = genreTitles(book.Genres)
			if h.svc.CoversDir != "" {
				if file, ok := h.storeCoverForBook(ctx, bookID, snap, book); ok {
					snap.CoverFile = file
				}
			}
			if snap.ADBReleaseID != "" {
				if rel, err := h.getAudiobookDB().GetRelease(ctx, snap.ADBReleaseID, "chapterDetail"); err == nil && rel != nil && rel.ChapterDetail != nil {
					snap.Chapters = chaptersFromADB(rel.ChapterDetail.Chapters)
				} else if err != nil {
					log.Printf("snapshot %d: release chapters unavailable: %v", bookID, err)
				}
			}
		} else {
			log.Printf("snapshot %d: catalog unavailable, keeping previously captured catalog content: %v", bookID, err)
			catalogDegraded = true
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
	if s.CoverFile == "" {
		return
	}
	// The set of cover filenames that must survive: the current snapshot's
	// reference plus every active job's queued snapshot reference. If the
	// active-reference scan itself fails, do NOT prune at all: deleting on an
	// incomplete keep-set would remove a file some active job names, so on a
	// query error the sweep is skipped (orphan covers simply accumulate).
	keep := map[string]struct{}{s.CoverFile: {}}
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
	   authors, narrators, series_name, series_position, description, genres, year, cover_file, chapters, captured_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	 ON CONFLICT(book_id) DO UPDATE SET
	   audiobookdb_book_id=excluded.audiobookdb_book_id,
	   audiobookdb_release_id=excluded.audiobookdb_release_id,
	   title=excluded.title,
	   authors=excluded.authors, narrators=excluded.narrators,
	   series_name=excluded.series_name, series_position=excluded.series_position,
	   description=excluded.description, genres=excluded.genres, year=excluded.year,
	   cover_file=excluded.cover_file, chapters=excluded.chapters, captured_at=datetime('now')`,
		s.BookID, s.ADBBookID, s.ADBReleaseID, s.Title,
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
		adbBook, adbRelease, title, seriesName, seriesPosition, description string
		authorsJSON, narratorsJSON, genresJSON, chaptersJSON                string
		coverFile                                                           string
		year, bookIDCol                                                     sql.NullInt64
		capturedAt                                                          sql.NullString
	)
	err := db.QueryRow(
		`SELECT book_id, audiobookdb_book_id, audiobookdb_release_id, title,
		        authors, narrators, series_name, series_position, description, genres,
		        year, cover_file, chapters, captured_at
		 FROM book_snapshots WHERE book_id = ?`,
		bookID,
	).Scan(&bookIDCol, &adbBook, &adbRelease, &title, &authorsJSON,
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
		Title: title, SeriesName: seriesName,
		SeriesPosition: seriesPosition, Description: description,
		CoverFile: coverFile, CapturedAt: capturedAt.String,
	}
	if year.Valid {
		v := int(year.Int64)
		s.Year = &v
	}
	if err := json.Unmarshal([]byte(authorsJSON), &s.Authors); err != nil {
		s.Authors = []string{}
	}
	if err := json.Unmarshal([]byte(narratorsJSON), &s.Narrators); err != nil {
		s.Narrators = []string{}
	}
	if err := json.Unmarshal([]byte(genresJSON), &s.Genres); err != nil {
		s.Genres = []string{}
	}
	if err := json.Unmarshal([]byte(chaptersJSON), &s.Chapters); err != nil {
		s.Chapters = []SnapshotChapter{}
	}
	return s, nil
}

// buildEngineJob assembles the structured engine job from a captured
// snapshot: the effective metadata becomes input_supplied, the destination
// and path format come from the resolved queue-time pair, and the immutable
// chapter/artwork references are staged for this run. The cover policy is the
// only per-run filesystem step: the captured cover file is copied into the
// output root (the engine's asset root) under a job-owned hidden name, then
// referenced by absolute path, because the engine requires a local cover file
// to live under the job's output root. A missing or unreadable cover degrades
// to "clear" (no cover) rather than failing the run: the selection's naming
// and chapters are the load-bearing parts, and artwork loss is reported on
// the snapshot capture, not hidden here.
// buildEngineJob returns the job file plus the absolute path of the cover
// Bragi staged into the output root for this run ("" when none was staged);
// the caller removes that file after RunJob in every case, since the engine
// has already copied the bytes into the output on success.
func buildEngineJob(args jobArgs, jobID string, snap BookSnapshot, outputRoot, pathFormat string) (audio.JobFile, string, error) {
	// The engine requires a local cover file under its asset root: create
	// the root up front so staging cannot write into a directory that only
	// the engine later creates (or never creates on an early failure).
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return audio.JobFile{}, "", fmt.Errorf("create output root %s: %w", outputRoot, err)
	}
	supplied := snap.toSupplied()
	jf := audio.JobFile{
		SchemaVersion: audio.JobSchemaVersion,
		Inputs:        args.SrcPaths,
		InputSupplied: supplied,
		OutputRoot:    outputRoot,
		PathFormat:    audio.NormalizeEnginePathFormat(pathFormat),
		Policies:      audio.AllSupportedWritePolicyFor(supplied),
	}

	// Cover: stage the captured file under the asset root, or clear.
	jf.Cover = json.RawMessage(`"clear"`)
	var stagedCover string
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
			// No usable local cover: embed nothing rather than fetch at run
			// time (supplied mode is offline) or fail.
			log.Printf("stage cover %s for book %d: %v", snap.CoverFile, snap.BookID, cerr)
		}
	}

	// Chapters: embed the selected release's explicit chapter list when the
	// snapshot captured one (immutable chapter references — the selected
	// edition's chapters, not a re-derivation from input tracks); an empty
	// list is an explicit clear.
	switch {
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

// chaptersFromADB converts ADB chapter details to the stored integer-millisecond
// form the engine consumes.
func chaptersFromADB(chs []audiobookdb.Chapter) []SnapshotChapter {
	out := make([]SnapshotChapter, 0, len(chs))
	for _, c := range chs {
		out = append(out, SnapshotChapter{
			Title:          c.Title,
			StartMillis:    uint64(c.StartOffsetMs),
			DurationMillis: uint64(c.LengthMs),
		})
	}
	return out
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
func (h *Handler) storeCoverForBook(ctx context.Context, bookID int64, s *BookSnapshot, book *audiobookdb.Book) (string, bool) {
	if book == nil || len(book.Images) == 0 || book.Images[0].URL == "" {
		return "", false
	}
	name := fmt.Sprintf("%d-%d.jpg", bookID, time.Now().UnixNano())
	dest := filepath.Join(h.svc.CoversDir, name)
	dlCtx, cancel := context.WithTimeout(ctx, coverDownloadTimeout)
	defer cancel()
	if err := downloadCoverFile(dlCtx, book.Images[0].URL, dest); err != nil {
		log.Printf("snapshot %d: cover download: %v", bookID, err)
		os.Remove(dest) // only our own new file, never one a snapshot references
		return "", false
	}
	return name, true
}

// coverDownloadTimeout bounds a snapshot cover fetch so a slow catalog does
// not stall a save.
const coverDownloadTimeout = 15 * time.Second

// coverMaxBytes caps a snapshot cover fetch: a cover is a thumbnail, not a
// book. A URL that streams past this is not a cover; stop.
const coverMaxBytes = 8 << 20

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
func lookupCoverHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// assertPublicCoverURL refuses a cover destination that is not a public
// http(s) URL: private, loopback, link-local, multicast, unspecified, and
// carrier-grade NAT ranges are not public destinations, and a
// catalog-provided (or redirect-hopped) cover URL must never fetch from
// one. The host is resolved and EVERY address checked, because a name
// resolving to mixed records must not pass on the public one. Returns the
// pinned hop (first validated address) the fetch must connect to.
func assertPublicCoverURL(ctx context.Context, u *url.URL) (coverHop, error) {
	if u == nil {
		return coverHop{}, errors.New("empty cover URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return coverHop{}, fmt.Errorf("cover URL scheme %q is not http(s)", u.Scheme)
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
// unspecified (0.0.0.0, :: — locally routable on applicable systems), and
// the 100.64.0.0/10 shared address space, none of which a cover fetch may
// reach.
func isNonPublicCoverAddress(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return ip4InCGNAT(ip)
}

func ip4InCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
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
		return err
	}
	current := parsed
	for hop := 0; hop < 4; hop++ {
		hp, err := assertPublicCoverURL(ctx, current)
		if err != nil {
			return err
		}
		resp, err := doPinnedCoverGet(ctx, hp)
		if err != nil {
			return err
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
		return nil, err
	}
	return resp, nil
}

// writeCoverBody streams the response into dest atomically, capped.
func writeCoverBody(ctx context.Context, resp *http.Response, dest string) error {
	defer resp.Body.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, coverMaxBytes+1))
	if cl := out.Close(); err == nil {
		err = cl
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if n > coverMaxBytes {
		os.Remove(tmp)
		return fmt.Errorf("cover exceeds %d bytes", coverMaxBytes)
	}
	return os.Rename(tmp, dest)
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
