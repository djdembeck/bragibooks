package api

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

// sourceASINPattern is the canonical Audible ASIN form after
// normalization: uppercase, 11 characters.
var sourceASINPattern = regexp.MustCompile(`^B[A-Z0-9]{9}$`)

// sourceMetadataComplete reports whether the snapshot is a clean,
// complete extraction pass: correct schema, no truncation, and every
// discovered file probed. Only a complete snapshot may claim a field is
// genuinely missing from the source.
func sourceMetadataComplete(snap *audio.SourceMetadata) bool {
	return snap.Schema == audio.SourceMetadataSchema && snap.Complete()
}

// deriveSourceASIN projects one parsed source metadata snapshot onto the
// narrow SourceASIN view. It never reads the books table and never
// mutates the snapshot.
func deriveSourceASIN(snap *audio.SourceMetadata) *models.SourceASIN {
	if snap == nil {
		return nil
	}
	complete := sourceMetadataComplete(snap)

	field, ok := snap.Fields[audio.FieldASIN]
	if !ok {
		if complete {
			return &models.SourceASIN{Status: models.SourceASINMissing}
		}
		return &models.SourceASIN{Status: models.SourceASINUnknown}
	}

	switch field.Status {
	case audio.StatusError:
		return &models.SourceASIN{Status: models.SourceASINUnknown}
	case audio.StatusMissing:
		if len(field.Values) == 0 {
			if complete {
				return &models.SourceASIN{Status: models.SourceASINMissing}
			}
			return &models.SourceASIN{Status: models.SourceASINUnknown}
		}
		// Missing status with values is a contradictory combination.
		return &models.SourceASIN{Status: models.SourceASINUnknown}
	case audio.StatusPresent:
		return derivePresentSourceASIN(complete, &field)
	default:
		return &models.SourceASIN{Status: models.SourceASINUnknown}
	}
}

// normalizeASINValues uppercases and trims each raw value, keeps only
// canonical ASINs, dedupes, and preserves first-seen order.
func normalizeASINValues(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		v = strings.ToUpper(strings.TrimSpace(v))
		if !sourceASINPattern.MatchString(v) || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func derivePresentSourceASIN(complete bool, field *audio.SourceField) *models.SourceASIN {
	normalized := normalizeASINValues(field.Values)
	if len(normalized) == 0 {
		return &models.SourceASIN{Status: models.SourceASINUnknown}
	}

	capped := len(field.Values) >= audio.MaxDistinctValuesPerField
	if capped {
		// The retained value list hit the per-field cap, so the true
		// distinct count is unknown: never prefill, mark truncation.
		if len(normalized) > 1 {
			return &models.SourceASIN{Status: models.SourceASINAmbiguous, Count: len(normalized), CountTruncated: true}
		}
		return &models.SourceASIN{Status: models.SourceASINUnknown, CountTruncated: true}
	}

	if !complete {
		if len(normalized) > 1 {
			// The pass was incomplete, so the retained list cannot claim an
			// exact count even when it did not hit the per-field cap.
			return &models.SourceASIN{Status: models.SourceASINAmbiguous, Count: len(normalized), CountTruncated: true}
		}
		return &models.SourceASIN{Status: models.SourceASINUnknown}
	}

	if len(normalized) == 1 {
		source := field.Source
		if source != audio.SourceEmbedded && source != audio.SourceFilename {
			source = ""
		}
		return &models.SourceASIN{
			Status: models.SourceASINPresent,
			Value:  normalized[0],
			Count:  1,
			Source: source,
		}
	}
	return &models.SourceASIN{Status: models.SourceASINAmbiguous, Count: len(normalized)}
}

// attachSourceASINs batch-loads book_source_metadata for the given books
// and sets SourceASIN on each. Best-effort: a missing or malformed row
// leaves SourceASIN nil, and a query failure logs and leaves everything
// nil — it never fails the list.
func (h *Handler) attachSourceASINs(books []models.BookWithPeople) {
	if len(books) == 0 {
		return
	}

	placeholders := make([]string, len(books))
	args := make([]any, len(books))
	byID := make(map[int64]*models.BookWithPeople, len(books))
	for i := range books {
		placeholders[i] = "?"
		args[i] = books[i].ID
		byID[books[i].ID] = &books[i]
	}

	rows, err := h.svc.DB.Query(
		fmt.Sprintf("SELECT book_id, payload FROM book_source_metadata WHERE book_id IN (%s)", strings.Join(placeholders, ",")),
		args...,
	)
	if err != nil {
		log.Printf("attach source ASINs: query book_source_metadata: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var bookID int64
		var payload []byte
		if err := rows.Scan(&bookID, &payload); err != nil {
			log.Printf("attach source ASINs: scan row: %v", err)
			continue
		}
		var snap audio.SourceMetadata
		if err := json.Unmarshal(payload, &snap); err != nil {
			// Malformed payload: leave the book's SourceASIN nil.
			continue
		}
		if book, ok := byID[bookID]; ok {
			book.SourceASIN = deriveSourceASIN(&snap)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("attach source ASINs: read rows: %v", err)
	}
}
