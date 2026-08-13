// Package analyticsfile discovers and reads TwinCAT Analytics Logger storage
// folders (.tas symbol files + .tay data files + Analytics.db index).
//
// A storage folder has the layout:
//
// <root>/
//
//	<record-name>/
//	  Record_N/
//	    <seq>/
//	      <UUID>/
//	        Analytics.db   — SQLite index (files + indexes tables)
//	        Analytics.tas  — symbol stream (binary)
//	        Analytics-<timestamp>#<record-name>.tay  — data files
//
// DiscoverStreams walks root recursively looking for Analytics.db files.
// Each database represents one stream (one UUID folder).
package analyticsfile

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
	_ "modernc.org/sqlite"
)

// Stream describes one discovered analytics stream.
type Stream struct {
	// GUID is the stream's UUID folder name.
	GUID string

	// RecordName is the record name from Analytics.db (e.g. "Record_1").
	RecordName string

	// FieldCount is the number of decoded fields in this stream's layout.
	FieldCount int

	// FileCount is the total number of .tay files indexed in the database.
	FileCount int

	// StartTime is the earliest streamStart across all indexed files.
	StartTime time.Time

	// EndTime is the latest streamStop across all indexed files.
	EndTime time.Time

	// CycleTime is the PLC cycle time from the first file entry.
	CycleTime time.Duration

	// dir is the absolute path to the UUID folder containing the stream files.
	dir string

	// lay is the decoded field layout from the .tas file.
	lay *layout.Layout
}

// FileRef holds the metadata for one .tay file belonging to a stream.
type FileRef struct {
	// FileName is the base name of the .tay file (as stored in Analytics.db).
	FileName string

	// AbsPath is the absolute path to the .tay file on disk.
	AbsPath string

	// StreamStart is the UTC time of the first sample in this file.
	StreamStart time.Time

	// StreamStop is the UTC time of the last sample in this file.
	StreamStop time.Time

	// CycleTime is the PLC cycle time for this file.
	CycleTime time.Duration
}

// Layout returns the stream's decoded symbol layout.
func (s *Stream) Layout() *layout.Layout { return s.lay }

// Files returns the FileRefs for this stream, optionally filtered by time range.
// Pass nil for from and/or until to leave that bound open.
func (s *Stream) Files(from, until *time.Time) ([]FileRef, error) {
	db, err := sql.Open("sqlite", s.dir+"/Analytics.db")
	if err != nil {
		return nil, fmt.Errorf("analyticsfile: open db %s: %w", s.dir, err)
	}
	defer db.Close()

	query := `SELECT fileName, streamStart, streamStop, cycleTime FROM files`
	args := []any{}
	conditions := []string{}

	if until != nil {
		conditions = append(conditions, "streamStart <= ?")
		args = append(args, timeToWindowsFileTime(*until))
	}
	if from != nil {
		conditions = append(conditions, "streamStop >= ?")
		args = append(args, timeToWindowsFileTime(*from))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY streamStart ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsfile: query files: %w", err)
	}
	defer rows.Close()

	var refs []FileRef
	for rows.Next() {
		var name string
		var start, stop, cycle int64
		if err := rows.Scan(&name, &start, &stop, &cycle); err != nil {
			return nil, fmt.Errorf("analyticsfile: scan row: %w", err)
		}
		base := name
		if !strings.HasSuffix(base, ".tay") {
			base += ".tay"
		}
		refs = append(refs, FileRef{
			FileName:    base,
			AbsPath:     filepath.Join(s.dir, base),
			StreamStart: layout.WindowsFileTimeToUTC(uint64(start)),
			StreamStop:  layout.WindowsFileTimeToUTC(uint64(stop)),
			CycleTime:   time.Duration(cycle) * 100,
		})
	}
	return refs, rows.Err()
}

// DiscoverStreams walks root recursively, locates every Analytics.db file, and
// returns a Stream descriptor for each one.
func DiscoverStreams(root string) ([]Stream, error) {
	var streams []Stream

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "Analytics.db" {
			return nil
		}
		dir := filepath.Dir(path)
		s, err := loadStream(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "analyticsfile: skip %s: %v\n", dir, err)
			return nil
		}
		streams = append(streams, s)
		return nil
	})
	return streams, err
}

// loadStream reads the Analytics.db and Analytics.tas from dir and returns
// a fully populated Stream.
func loadStream(dir string) (Stream, error) {
	dbPath := filepath.Join(dir, "Analytics.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return Stream{}, fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	var count int
	var minStart, maxStop, minCycle int64
	var recordName string
	row := db.QueryRow(`
SELECT COUNT(*), MIN(streamStart), MAX(streamStop), MIN(cycleTime), MIN(recordName)
FROM files`)
	if err := row.Scan(&count, &minStart, &maxStop, &minCycle, &recordName); err != nil {
		return Stream{}, fmt.Errorf("query aggregate: %w", err)
	}

	guid := strings.ToLower(filepath.Base(dir))

	tasPath := filepath.Join(dir, "Analytics.tas")
	tasData, err := os.ReadFile(tasPath)
	if err != nil {
		return Stream{}, fmt.Errorf("read .tas: %w", err)
	}
	sym, err := parser.ParseSymbolStream(tasData)
	if err != nil {
		return Stream{}, fmt.Errorf("parse .tas: %w", err)
	}
	lay := layout.NewLayoutFromStream(sym)

	return Stream{
		GUID:       guid,
		RecordName: recordName,
		FieldCount: len(lay.Fields),
		FileCount:  count,
		StartTime:  layout.WindowsFileTimeToUTC(uint64(minStart)),
		EndTime:    layout.WindowsFileTimeToUTC(uint64(maxStop)),
		CycleTime:  time.Duration(minCycle) * 100,
		dir:        dir,
		lay:        lay,
	}, nil
}

// timeToWindowsFileTime converts a Go time.Time to a Windows FILETIME integer
// (100-nanosecond ticks since 1601-01-01 UTC).
func timeToWindowsFileTime(t time.Time) int64 {
	const windowsToUnixTicks int64 = 116_444_736_000_000_000
	ticks := t.UnixNano() / 100
	return ticks + windowsToUnixTicks
}
