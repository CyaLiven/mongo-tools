package mongoimport

import (
	"bufio"
	"fmt"
	"gopkg.in/mgo.v2/bson"
	"io"
	"strings"
)

const (
	entryDelimiter = '\n'
	tokenSeparator = "\t"
)

// TSVInputReader is a struct that implements the InputReader interface for a
// TSV input source
type TSVInputReader struct {
	// fields is a list of field names in the BSON documents to be imported
	fields []string

	// tsvReader is the underlying reader used to read data in from the TSV
	// or TSV file
	tsvReader *bufio.Reader

	// tsvRecord stores each line of input we read from the underlying reader
	tsvRecord string

	// numProcessed tracks the number of TSV records processed by the underlying reader
	numProcessed uint64

	// numDecoders is the number of concurrent goroutines to use for decoding
	numDecoders int

	// embedded sizeTracker exposes the Size() method to check the number of bytes read so far
	sizeTracker
}

// TSVConvertibleDoc implements the ConvertibleDoc interface for TSV input
type TSVConvertibleDoc struct {
	fields []string
	data   string
	index  uint64
}

// NewTSVInputReader returns a TSVInputReader configured to read input from the
// given io.Reader, extracting the specified fields only.
func NewTSVInputReader(fields []string, in io.Reader, numDecoders int) *TSVInputReader {
	szCount := &sizeTrackingReader{in, 0}
	return &TSVInputReader{
		fields:       fields,
		tsvReader:    bufio.NewReader(in),
		numProcessed: uint64(0),
		numDecoders:  numDecoders,
		sizeTracker:  szCount,
	}
}

// ReadAndValidateHeader sets the import fields for a TSV importer
func (tsvInputReader *TSVInputReader) ReadAndValidateHeader() (err error) {
	header, err := tsvInputReader.tsvReader.ReadString(entryDelimiter)
	if err != nil {
		return err
	}
	for _, field := range strings.Split(header, tokenSeparator) {
		tsvInputReader.fields = append(tsvInputReader.fields, strings.TrimRight(field, "\r\n"))
	}
	return validateReaderFields(tsvInputReader.fields)
}

// StreamDocument takes in two channels: it sends processed documents on the
// readDocChan channel and if any error is encountered, the error is sent on the
// errChan channel. It keeps reading from the underlying input source until it
// hits EOF or an error. If ordered is true, it streams the documents in which
// the documents are read
func (tsvInputReader *TSVInputReader) StreamDocument(ordered bool, readDocChan chan bson.D, errChan chan error) {
	tsvRecordChan := make(chan ConvertibleDoc, tsvInputReader.numDecoders)
	go func() {
		var err error
		for {
			tsvInputReader.tsvRecord, err = tsvInputReader.tsvReader.ReadString(entryDelimiter)
			if err != nil {
				if err != io.EOF {
					tsvInputReader.numProcessed++
					errChan <- fmt.Errorf("read error on entry #%v: %v", tsvInputReader.numProcessed, err)
				}
				close(tsvRecordChan)
				return
			}
			tsvRecordChan <- TSVConvertibleDoc{
				fields: tsvInputReader.fields,
				data:   tsvInputReader.tsvRecord,
				index:  tsvInputReader.numProcessed,
			}
			tsvInputReader.numProcessed++
		}
	}()
	errChan <- streamDocuments(ordered, tsvInputReader.numDecoders, tsvRecordChan, readDocChan)
}

// This is required to satisfy the ConvertibleDoc interface for TSV input. It
// does TSV-specific processing to convert the TSVConvertibleDoc to a bson.D
func (tsvConvertibleDoc TSVConvertibleDoc) Convert() (bson.D, error) {
	tsvTokens := strings.Split(
		strings.TrimRight(tsvConvertibleDoc.data, "\r\n"),
		tokenSeparator,
	)
	return tokensToBSON(
		tsvConvertibleDoc.fields,
		tsvTokens,
		tsvConvertibleDoc.index,
	)
}
