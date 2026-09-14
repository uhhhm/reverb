package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/recommend/listenbrainz"
	"github.com/uhhhm/reverb/internal/recommend/quality"
)

const (
	defaultFixture  = "internal/recommend/quality/testdata/history.json"
	defaultBaseline = "internal/recommend/quality/testdata/baseline.json"
	// defaultTimeout bounds a whole run. Recording spaces ListenBrainz
	// requests a second apart, so it leaves room for a large history.
	defaultTimeout = 30 * time.Minute
)

// newSource is the source -record-cache captures (test seam).
var newSource = func() recommend.TrackSimilarity { return listenbrainz.New() }

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("recommend-quality", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fixturePath := flags.String("fixture", defaultFixture, "anonymised history and recorded network responses")
	databasePath := flags.String("db", "", "use play history from this Reverb database")
	baselinePath := flags.String("baseline", defaultBaseline, "baseline report to compare against")
	recordCache := flags.String("record-cache", "", "capture ListenBrainz responses to this fixture (requires -db)")
	writeBaseline := flags.Bool("write-baseline", false, "replace the baseline with the current fixture report")
	timeout := flags.Duration("timeout", defaultTimeout, "give up on the whole run after this long")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	defer func() {
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s (raise -timeout): %w", *timeout, err)
		}
	}()
	fixtureExplicit := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "fixture" {
			fixtureExplicit = true
		}
	})
	if *databasePath != "" && *recordCache == "" && !fixtureExplicit {
		return fmt.Errorf("-db requires either -record-cache to capture matching responses or an explicit -fixture to replay them")
	}

	fixture, err := quality.LoadFixture(*fixturePath)
	if err != nil {
		return fmt.Errorf("load fixture: %w", err)
	}
	if *databasePath != "" {
		fixture.Plays, err = quality.LoadDatabase(ctx, *databasePath)
		if err != nil {
			return fmt.Errorf("load database: %w", err)
		}
		fixture.Name = "database"
	}
	if *recordCache != "" {
		if *databasePath == "" {
			return fmt.Errorf("-record-cache requires -db")
		}
		fixture.Sources = nil
		fixture, err = quality.RecordSource(ctx, fixture, newSource())
		if err != nil {
			return fmt.Errorf("record ListenBrainz cache: %w", err)
		}
		if err := quality.WriteFixture(*recordCache, fixture); err != nil {
			return fmt.Errorf("write recorded cache: %w", err)
		}
		fmt.Fprintf(stderr, "saved replayable fixture to %s\n", *recordCache)
	}
	report, err := quality.Evaluate(ctx, fixture)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}

	if *writeBaseline {
		if *databasePath != "" {
			return fmt.Errorf("refusing to save a personal database result as the checked-in baseline")
		}
		if err := quality.WriteReport(*baselinePath, report); err != nil {
			return fmt.Errorf("write baseline: %w", err)
		}
		fmt.Fprintf(stderr, "saved baseline to %s\n", *baselinePath)
		return nil
	}
	if *databasePath != "" {
		return nil
	}
	baseline, err := quality.LoadReport(*baselinePath)
	if err != nil {
		return fmt.Errorf("load baseline: %w", err)
	}
	if regressions := quality.Compare(report, baseline); len(regressions) != 0 {
		for _, regression := range regressions {
			fmt.Fprintln(stderr, regression)
		}
		return fmt.Errorf("recommendation quality regressed")
	}
	fmt.Fprintln(stderr, "recommendation quality meets the checked-in baseline")
	return nil
}
