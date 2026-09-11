package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/uhhhm/reverb/internal/recommend/quality"
)

const (
	defaultFixture  = "internal/recommend/quality/testdata/history.json"
	defaultBaseline = "internal/recommend/quality/testdata/baseline.json"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("recommend-quality", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fixturePath := flags.String("fixture", defaultFixture, "anonymised history and recorded network responses")
	databasePath := flags.String("db", "", "use play history from this Reverb database")
	baselinePath := flags.String("baseline", defaultBaseline, "baseline report to compare against")
	writeBaseline := flags.Bool("write-baseline", false, "replace the baseline with the current fixture report")
	if err := flags.Parse(args); err != nil {
		return err
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
