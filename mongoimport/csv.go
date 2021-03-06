package mongoimport

import (
	"fmt"
	"github.com/mongodb/mongo-tools/mongoimport/csv"
	"gopkg.in/mgo.v2/bson"
	"io"
)

// CSVInputReader is a struct that implements the InputReader interface for a
// CSV input source
type CSVInputReader struct {

	// fields is a list of field names in the BSON documents to be imported
	fields []string

	// csvReader is the underlying reader used to read data in from the CSV or CSV file
	csvReader *csv.Reader

	// csvRecord stores each line of input we read from the underlying reader
	csvRecord []string

	// numProcessed tracks the number of CSV records processed by the underlying reader
	numProcessed uint64

	// numDecoders is the number of concurrent goroutines to use for decoding
	numDecoders int

	// embedded sizeTracker exposes the Size() method to check the number of bytes read so far
	sizeTracker
}

// CSVConvertibleDoc implements the ConvertibleDoc interface for CSV input
type CSVConvertibleDoc struct {
	fields, data []string
	index        uint64
}

// NewCSVInputReader returns a CSVInputReader configured to read input from the
// given io.Reader, extracting the specified fields only.
func NewCSVInputReader(fields []string, in io.Reader, numDecoders int) *CSVInputReader {
	szCount := &sizeTrackingReader{in, 0}
	csvReader := csv.NewReader(szCount)
	// allow variable number of fields in document
	csvReader.FieldsPerRecord = -1
	csvReader.TrimLeadingSpace = true
	return &CSVInputReader{
		fields:       fields,
		csvReader:    csvReader,
		numProcessed: uint64(0),
		numDecoders:  numDecoders,
		sizeTracker:  szCount,
	}
}

// ReadAndValidateHeader sets the import fields for a CSV importer
func (csvInputReader *CSVInputReader) ReadAndValidateHeader() (err error) {
	fields, err := csvInputReader.csvReader.Read()
	if err != nil {
		return err
	}
	csvInputReader.fields = fields
	return validateReaderFields(csvInputReader.fields)
}

// StreamDocument takes in two channels: it sends processed documents on the
// readDocChan channel and if any error is encountered, the error is sent on the
// errChan channel. It keeps reading from the underlying input source until it
// hits EOF or an error. If ordered is true, it streams the documents in which
// the documents are read
func (csvInputReader *CSVInputReader) StreamDocument(ordered bool, readDocChan chan bson.D, errChan chan error) {
	csvRecordChan := make(chan ConvertibleDoc, csvInputReader.numDecoders)
	go func() {
		var err error
		for {
			csvInputReader.csvRecord, err = csvInputReader.csvReader.Read()
			if err != nil {
				if err != io.EOF {
					csvInputReader.numProcessed++
					errChan <- fmt.Errorf("read error on entry #%v: %v", csvInputReader.numProcessed, err)
				}
				close(csvRecordChan)
				return
			}
			csvRecordChan <- CSVConvertibleDoc{
				fields: csvInputReader.fields,
				data:   csvInputReader.csvRecord,
				index:  csvInputReader.numProcessed,
			}
			csvInputReader.numProcessed++
		}
	}()
	errChan <- streamDocuments(ordered, csvInputReader.numDecoders, csvRecordChan, readDocChan)
}

// This is required to satisfy the ConvertibleDoc interface for CSV input. It
// does CSV-specific processing to convert the CSVConvertibleDoc to a bson.D
func (csvConvertibleDoc CSVConvertibleDoc) Convert() (bson.D, error) {
	return tokensToBSON(
		csvConvertibleDoc.fields,
		csvConvertibleDoc.data,
		csvConvertibleDoc.index,
	)
}
