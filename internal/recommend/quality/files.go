package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/uhhhm/reverb/internal/store"
)

// LoadFixture reads anonymised history and recorded source responses.
func LoadFixture(path string) (Fixture, error) {
	var fixture Fixture
	if err := loadJSON(path, &fixture); err != nil {
		return Fixture{}, err
	}
	return fixture, nil
}

// LoadReport reads a checked-in quality baseline.
func LoadReport(path string) (Report, error) {
	var report Report
	if err := loadJSON(path, &report); err != nil {
		return Report{}, err
	}
	return report, nil
}

// WriteFixture saves history and captured source responses for deterministic
// replay on later evaluator runs.
func WriteFixture(path string, fixture Fixture) error {
	return writeJSON(path, fixture)
}

func loadJSON(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// WriteReport saves stable, indented JSON for review and future comparisons.
func WriteReport(path string, report Report) error {
	return writeJSON(path, report)
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

// Compare returns metric regressions from baseline. Ranking work can improve a
// surface and then deliberately replace the baseline with WriteReport.
func Compare(current, baseline Report) []string {
	var regressions []string
	for surface, want := range baseline.Surfaces {
		got, ok := current.Surfaces[surface]
		if !ok {
			regressions = append(regressions, surface+": missing")
			continue
		}
		for name, values := range map[string][2]float64{
			"hitRate":   {got.HitRate, want.HitRate},
			"recallAtK": {got.RecallAtK, want.RecallAtK},
			"ndcgAtK":   {got.NDCGAtK, want.NDCGAtK},
		} {
			if values[0]+1e-12 < values[1] {
				regressions = append(regressions, fmt.Sprintf("%s.%s %.6f < baseline %.6f", surface, name, values[0], values[1]))
			}
		}
	}
	return regressions
}

// LoadDatabase replaces fixture history with plays and canonical identities
// from a real Reverb database. Opening without migration keeps evaluation
// read-only with respect to the user's schema and data.
func LoadDatabase(ctx context.Context, path string) ([]Play, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	st, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	rows, err := st.Q().ListAllPlays(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Play, 0, len(rows))
	for _, row := range rows {
		entity, err := st.Q().GetCatalogEntity(ctx, row.CatalogID)
		if err != nil {
			return nil, fmt.Errorf("catalog entity %s: %w", row.CatalogID, err)
		}
		out = append(out, Play{
			CatalogID: row.CatalogID, Artist: entity.Artist, Title: entity.Title,
			MBID: entity.Mbid, PlayedAt: row.PlayedAt,
		})
	}
	return out, nil
}
