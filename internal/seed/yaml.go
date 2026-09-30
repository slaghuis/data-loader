package seed

import (
    "fmt"
    "os"

    "gopkg.in/yaml.v3"
)

// File is the on-disk shape of a seed file.
type File struct {
    Sources    []SourceSpec    `yaml:"sources"`
    Loads      []LoadSpec      `yaml:"loads"`
    ModbusTags []ModbusTagSpec `yaml:"modbus_tags"`
    OPCUANodes []OPCUANodeSpec `yaml:"opcua_nodes"`
}

type SourceSpec struct {
    Name        string            `yaml:"name"`
    Kind        string            `yaml:"kind"`
    Description string            `yaml:"description"`
    IsEnabled   *bool             `yaml:"is_enabled"`
    Params      map[string]string `yaml:"params"`
}

type LoadSpec struct {
    Name            string `yaml:"name"`
    Source          string `yaml:"source"`
    CronExpression  string `yaml:"cron_expression"`
    Mode            string `yaml:"mode"`
    ObjectName      string `yaml:"object_name"`
    WatermarkColumn string `yaml:"watermark_column"`
    WatermarkType   string `yaml:"watermark_type"`
    TargetSchema    string `yaml:"target_schema"`
    TargetTable     string `yaml:"target_table"`
    BatchSize       int    `yaml:"batch_size"`
    AutoCreateTable *bool  `yaml:"auto_create_table"`
    IsEnabled       *bool  `yaml:"is_enabled"`
}

type ModbusTagSpec struct {
    Load          string   `yaml:"load"`           // references LoadSpec.Name
    TagName       string   `yaml:"tag_name"`
    CorrelationID string   `yaml:"correlation_id"`
    UnitID        uint8    `yaml:"unit_id"`
    RegisterType  string   `yaml:"register_type"`
    Address       uint16   `yaml:"address"`
    Quantity      uint16   `yaml:"quantity"`
    DataType      string   `yaml:"data_type"`
    ByteOrder     string   `yaml:"byte_order"`
    WordOrder     string   `yaml:"word_order"`
    Scale         *float64 `yaml:"scale"`
    Offset        *float64 `yaml:"offset"`
    IsEnabled     *bool    `yaml:"is_enabled"`
}

type OPCUANodeSpec struct {
    Load      string   `yaml:"load"`               // references LoadSpec.Name
    TagName   string   `yaml:"tag_name"`
    NodeID    string   `yaml:"node_id"`
    Scale     *float64 `yaml:"scale"`
    Offset    *float64 `yaml:"offset"`
    IsEnabled *bool    `yaml:"is_enabled"`
}

// Load reads and parses a seed file. It does not validate references.
func Load(path string) (*File, error) {
    b, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read seed file: %w", err)
    }
    var f File
    dec := yaml.NewDecoder(newStrictReader(b))
    dec.KnownFields(true) // fail on unknown keys — catches typos early
    if err := dec.Decode(&f); err != nil {
        return nil, fmt.Errorf("parse seed file: %w", err)
    }
    return &f, nil
}

// newStrictReader is a trivial wrapper — kept as a hook if you later want to
// preprocess (env-var expansion, includes, etc.).
func newStrictReader(b []byte) *bytesReader { return &bytesReader{b: b} }

type bytesReader struct {
    b []byte
    i int
}

func (r *bytesReader) Read(p []byte) (int, error) {
    if r.i >= len(r.b) {
        return 0, fmtEOF
    }
    n := copy(p, r.b[r.i:])
    r.i += n
    return n, nil
}

var fmtEOF = fmtErr("EOF")

type fmtErr string

func (e fmtErr) Error() string { return string(e) }
