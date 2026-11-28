package api

import (
	"regexp"
	"strings"

	"github.com/bragibooks/bragibooks/internal/audio"
	"github.com/bragibooks/bragibooks/internal/models"
)

// sourceASINPattern is the canonical Audible ASIN form after
// normalization: uppercase, 11 characters.
var sourceASINPattern = regexp.MustCompile(`^B[A-Z0-9]{9}$`)

// sourceMetadataComplete reports whether the snapshot is a clean,
// complete extraction pass: current schema, no truncation, and every
// discovered file probed. Only a complete snapshot may claim a field is
// genuinely missing from the source.
//
// The schema check is the conservative half of the upgrade contract: a
// schema-1 payload predates the snapshot Degraded flag, so its stat-failure
// rows decode with Degraded == false and files_total/files_probed == 0,
// which Complete() alone would accept. Rejecting every older schema makes
// those rows derive "unknown" until the book is reprocessed.
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

	if field.Truncated {
		// The per-field cap dropped at least one further distinct value, so
		// the true distinct count is unknown: never prefill, mark
		// truncation. A truncated-but-single value still degrades to
		// "unknown" on purpose — the dropped value may have been a
		// differing ASIN, and a retained-count of one is not evidence of
		// agreement.
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
