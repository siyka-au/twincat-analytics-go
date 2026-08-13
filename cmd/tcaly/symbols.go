package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/siyka-au/twincat-analytics-go/internal/analyticsfile"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

var alyListSymbolsCmd = &cobra.Command{
	Use:               "list-symbols <guid>",
	Short:             "Show the field layout for a stream",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: streamGUIDCompletions,
	RunE:              runAlyListSymbols,
}

func runAlyListSymbols(cmd *cobra.Command, args []string) error {
	streams, err := analyticsfile.DiscoverStreams(alyStorageFolder)
	if err != nil {
		return fmt.Errorf("discover streams: %w", err)
	}
	stream, err := findStream(streams, args[0])
	if err != nil {
		return err
	}

	lay := stream.Layout()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Stream:  %s  (%s)\n", stream.GUID, stream.RecordName)
	fmt.Fprintf(tw, "Fields:  %d\n", len(lay.Fields))
	fmt.Fprintf(tw, "Sample:  %d bytes\n\n", lay.SampleDataSize)
	fmt.Fprintln(tw, "Name\tType\tOffset\tSize\tDataType")
	fmt.Fprintln(tw, "----\t----\t------\t----\t--------")
	for _, f := range lay.Fields {
		fmt.Fprintf(tw, "%s\t%s\t0x%04X\t%d\t%s\n",
			f.Name,
			f.TypeName,
			f.RelativeOffset,
			f.Size,
			adsDataTypeName(f.DataType),
		)
	}
	return tw.Flush()
}

func adsDataTypeName(dt parser.AdsDataType) string {
	switch dt {
	case parser.AdsDataTypeVoid:
		return "Void"
	case parser.AdsDataTypeInt8:
		return "Int8"
	case parser.AdsDataTypeUint8:
		return "Uint8"
	case parser.AdsDataTypeInt16:
		return "Int16"
	case parser.AdsDataTypeUint16:
		return "Uint16"
	case parser.AdsDataTypeInt32:
		return "Int32"
	case parser.AdsDataTypeUint32:
		return "Uint32"
	case parser.AdsDataTypeInt64:
		return "Int64"
	case parser.AdsDataTypeUint64:
		return "Uint64"
	case parser.AdsDataTypeReal32:
		return "Real32"
	case parser.AdsDataTypeReal64:
		return "Real64"
	case parser.AdsDataTypeString:
		return "String"
	case parser.AdsDataTypeWString:
		return "WString"
	case parser.AdsDataTypeBit:
		return "Bit"
	case parser.AdsDataTypeBigType:
		return "BigType"
	default:
		return fmt.Sprintf("0x%02X", uint32(dt))
	}
}
