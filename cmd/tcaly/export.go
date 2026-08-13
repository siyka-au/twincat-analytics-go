package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/siyka-au/twincat-analytics-go/internal/analyticsfile"
	parquetexport "github.com/siyka-au/twincat-analytics-go/internal/export/parquet"
	"github.com/siyka-au/twincat-analytics-go/layout"
)

var (
	exportStream      string
	exportOutput      string
	exportFrom        string
	exportUntil       string
	exportCompression string
)

var alyExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export a stream to Parquet",
	RunE:  runAlyExport,
}

func init() {
	alyExportCmd.Flags().StringVar(&exportStream, "stream", "", "Stream GUID or record name to export (required)")
	alyExportCmd.Flags().StringVar(&exportOutput, "output", "", "Output .parquet file path (required)")
	alyExportCmd.Flags().StringVar(&exportFrom, "from", "", "Export from this time inclusive (RFC3339, e.g. 2026-03-04T03:30:00Z)")
	alyExportCmd.Flags().StringVar(&exportUntil, "until", "", "Export up to this time inclusive (RFC3339)")
	alyExportCmd.Flags().StringVar(&exportCompression, "compression", "snappy", "Parquet compression codec (uncompressed, snappy, gzip, zstd, lz4raw, brotli)")
	_ = alyExportCmd.RegisterFlagCompletionFunc("stream", streamGUIDCompletions)
	_ = alyExportCmd.RegisterFlagCompletionFunc("compression", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"uncompressed", "snappy", "gzip", "zstd", "lz4raw", "brotli"}, cobra.ShellCompDirectiveNoFileComp
	})
}

func runAlyExport(cmd *cobra.Command, args []string) error {
	if exportStream == "" {
		return fmt.Errorf("--stream is required")
	}
	if exportOutput == "" {
		return fmt.Errorf("--output is required")
	}

	var from, until *time.Time
	if exportFrom != "" {
		t, err := time.Parse(time.RFC3339, exportFrom)
		if err != nil {
			return fmt.Errorf("--from: %w", err)
		}
		from = &t
	}
	if exportUntil != "" {
		t, err := time.Parse(time.RFC3339, exportUntil)
		if err != nil {
			return fmt.Errorf("--until: %w", err)
		}
		until = &t
	}

	streams, err := analyticsfile.DiscoverStreams(alyStorageFolder)
	if err != nil {
		return fmt.Errorf("discover streams: %w", err)
	}
	stream, err := findStream(streams, exportStream)
	if err != nil {
		return err
	}

	files, err := stream.Files(from, until)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no files found for stream %q in the requested time range", exportStream)
	}

	out, err := os.Create(exportOutput)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	lay := stream.Layout()
	codec, err := parquetexport.CompressionFromString(exportCompression)
	if err != nil {
		return fmt.Errorf("--compression: %w", err)
	}
	pw, err := parquetexport.New(out, lay.Fields, codec)
	if err != nil {
		return fmt.Errorf("open parquet writer: %w", err)
	}

	var totalRows int64
	for _, f := range files {
		n, err := exportFile(f, lay, pw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skip %s: %v\n", f.FileName, err)
			continue
		}
		totalRows += n
	}

	if err := pw.Close(); err != nil {
		return fmt.Errorf("close parquet writer: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Exported %d rows from %d files to %s\n", totalRows, len(files), exportOutput)
	return nil
}

func exportFile(f analyticsfile.FileRef, lay *layout.Layout, pw *parquetexport.Writer) (int64, error) {
	data, err := os.ReadFile(f.AbsPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", f.AbsPath, err)
	}

	msg, err := layout.ParseDataMessage(data)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", f.AbsPath, err)
	}

	var baseTS time.Time
	if msg.HeadTimestamp != nil {
		baseTS = *msg.HeadTimestamp
	} else if msg.Header.StartTime != nil {
		baseTS = *msg.Header.StartTime
	}

	var cycle time.Duration
	if msg.Header.CycleTimeNs != nil {
		cycle = time.Duration(*msg.Header.CycleTimeNs)
	} else {
		cycle = time.Duration(msg.Header.CycleTime) * 100
	}

	var rows int64
	for i, sample := range msg.Samples {
		var ts time.Time
		if sample.Timestamp != nil {
			ts = *sample.Timestamp
		} else {
			ts = baseTS.Add(time.Duration(i) * cycle)
		}
		values := lay.ParseSample(sample.Raw)
		if err := pw.WriteRow(ts, values); err != nil {
			return rows, fmt.Errorf("write row %d: %w", i, err)
		}
		rows++
	}
	return rows, nil
}

func findStream(streams []analyticsfile.Stream, target string) (analyticsfile.Stream, error) {
	for _, s := range streams {
		if s.GUID == target || s.RecordName == target {
			return s, nil
		}
	}
	var matches []analyticsfile.Stream
	for _, s := range streams {
		if len(target) >= 4 && len(s.GUID) >= len(target) && s.GUID[:len(target)] == target {
			matches = append(matches, s)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return analyticsfile.Stream{}, fmt.Errorf("stream prefix %q is ambiguous (%d matches), use full GUID", target, len(matches))
	}
	return analyticsfile.Stream{}, fmt.Errorf("stream %q not found; run 'tcaly aly list-streams' to see available streams", target)
}
