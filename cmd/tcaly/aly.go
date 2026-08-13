package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/siyka-au/twincat-analytics-go/internal/analyticsfile"
)

var alyStorageFolder string

var alyCmd = &cobra.Command{
	Use:   "aly",
	Short: "Work with TwinCAT Analytics Logger storage folders",
}

var alyListCmd = &cobra.Command{
	Use:   "list-streams",
	Short: "List all streams found in the storage folder",
	RunE:  runAlyList,
}

func init() {
	alyCmd.PersistentFlags().StringVar(&alyStorageFolder, "storage-folder", ".", "Path to the Analytics Logger storage folder")
	alyCmd.AddCommand(alyListCmd)
	alyCmd.AddCommand(alyExportCmd)
	alyCmd.AddCommand(alyListSymbolsCmd)
	rootCmd.AddCommand(alyCmd)
}

// streamGUIDCompletions is a shared ValidArgsFunction that returns the lowercase
// GUIDs (with record name as description) of all streams in --storage-folder.
func streamGUIDCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	streams, err := analyticsfile.DiscoverStreams(alyStorageFolder)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	completions := make([]string, 0, len(streams))
	for _, s := range streams {
		completions = append(completions, s.GUID+"\t"+s.RecordName)
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

func runAlyList(cmd *cobra.Command, args []string) error {
	streams, err := analyticsfile.DiscoverStreams(alyStorageFolder)
	if err != nil {
		return fmt.Errorf("discover streams: %w", err)
	}
	if len(streams) == 0 {
		fmt.Fprintln(os.Stderr, "No streams found in", alyStorageFolder)
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GUID\tRecord\tFields\tFiles\tCycle\tStart (UTC)\tEnd (UTC)")
	fmt.Fprintln(tw, "----\t------\t------\t-----\t-----\t-----------\t---------")
	for _, s := range streams {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			s.GUID,
			s.RecordName,
			s.FieldCount,
			s.FileCount,
			formatDuration(s.CycleTime),
			s.StartTime.UTC().Format(time.RFC3339),
			s.EndTime.UTC().Format(time.RFC3339),
		)
	}
	return tw.Flush()
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fus", float64(d.Nanoseconds())/1000)
	default:
		return fmt.Sprintf("%.0fms", float64(d.Nanoseconds())/1_000_000)
	}
}
