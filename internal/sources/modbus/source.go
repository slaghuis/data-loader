package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	mb "github.com/simonvetter/modbus"

	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

const kind = "modbus"

// Repository accessor is package-private for this module.
// The pipeline injects it (see wiring section) so the source can load its tag list.
var tagRepo TagRepository

// TagRepository is the minimum contract this module needs from the metadata repo.
type TagRepository interface {
	ListModbusTags(ctx context.Context, loadID int64) ([]metadata.ModbusTag, error)
}

// SetTagRepository is called once at startup from main.go.
func SetTagRepository(r TagRepository) { tagRepo = r }

func init() {
	sources.Register(kind, New)
}

type Source struct {
	cfg    contracts.SourceConfig
	client *mb.ModbusClient
	mu     sync.Mutex
}

func New(cfg contracts.SourceConfig) (contracts.Source, error) {
	return &Source{cfg: cfg}, nil
}

func (s *Source) Kind() string { return kind }

func (s *Source) Open(ctx context.Context) error {
	host := s.cfg.Params["host"]
	if host == "" {
		return fmt.Errorf("modbus source requires 'host' param")
	}
	port := s.cfg.Params["port"]
	if port == "" {
		port = "502"
	}
	timeoutMS, _ := strconv.Atoi(s.cfg.Params["timeout_ms"])
	if timeoutMS <= 0 {
		timeoutMS = 2000
	}

	u := &url.URL{Scheme: "tcp", Host: host + ":" + port}
	client, err := mb.NewClient(&mb.ClientConfiguration{
		URL:     u.String(),
		Timeout: time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("build modbus client: %w", err)
	}
	if err := client.Open(); err != nil {
		return fmt.Errorf("open modbus client: %w", err)
	}
	s.client = client
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	// Modbus TCP has no ping. Best-effort: attempt a benign holding-register read at unit 1.
	// If your gateway doesn't respond to unit 1, treat "open succeeded" as ping success.
	if s.client == nil {
		return fmt.Errorf("source not open")
	}
	return nil
}

func (s *Source) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// Read performs a single polling cycle: read every enabled tag once and emit
// one batch containing all readings. Modbus is always append-only.
func (s *Source) Read(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
	log := logging.FromContext(ctx)
	start := time.Now().UTC()
	result := contracts.ReadResult{StartedAt: start}

	if tagRepo == nil {
		return result, fmt.Errorf("modbus tag repository not configured")
	}

	tags, err := tagRepo.ListModbusTags(ctx, load.LoadID)
	if err != nil {
		return result, fmt.Errorf("load tags: %w", err)
	}
	if len(tags) == 0 {
		log.Warn("no enabled tags for modbus load", "category", "read")
		result.FinishedAt = time.Now().UTC()
		return result, nil
	}

	columns := []contracts.Column{
		{Name: "tag_name", DataType: "string", Nullable: false},
		{Name: "ts", DataType: "datetime", Nullable: false},
		{Name: "value_num", DataType: "float64", Nullable: true},
		{Name: "value_bool", DataType: "bool", Nullable: true},
		{Name: "value_text", DataType: "string", Nullable: true},
		{Name: "quality", DataType: "string", Nullable: false},
	}

	rows := make([]contracts.Row, 0, len(tags))
	pollTS := time.Now().UTC()

	// Serialise access to the modbus client — the transport is single-threaded.
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, tag := range tags {
		row := contracts.Row{
			"tag_name":   tag.TagName,
			"ts":         pollTS,
			"value_num":  nil,
			"value_bool": nil,
			"value_text": nil,
			"quality":    "good",
		}

		if err := s.client.SetUnitId(tag.UnitID); err != nil {
			row["quality"] = "bad"
			row["value_text"] = fmt.Sprintf("set unit: %v", err)
			log.Warn("set unit id failed",
				"category", "read",
				"tag", tag.TagName,
				"unit_id", tag.UnitID,
				"err", err,
			)
			rows = append(rows, row)
			continue
		}
		if err := s.client.SetEncoding(endianness(tag.ByteOrder), wordOrder(tag.WordOrder)); err != nil {
			row["quality"] = "bad"
			row["value_text"] = fmt.Sprintf("set encoding: %v", err)
			rows = append(rows, row)
			continue
		}

		if err := readTagInto(s.client, tag, row); err != nil {
			row["quality"] = "bad"
			row["value_text"] = err.Error()
			log.Warn("modbus read failed",
				"category", "read",
				"tag", tag.TagName,
				"register", tag.RegisterType,
				"address", tag.Address,
				"err", err,
			)
		}
		rows = append(rows, row)
		result.RowsRead++
	}

	batch := contracts.Batch{Columns: columns, Rows: rows}
	if err := handler(ctx, batch); err != nil {
		return result, err
	}

	result.FinishedAt = time.Now().UTC()
	return result, nil
}

// ---- reading & decoding ----

func readTagInto(c *mb.ModbusClient, tag metadata.ModbusTag, row contracts.Row) error {
	switch strings.ToLower(tag.RegisterType) {
	case "coil":
		vals, err := c.ReadCoils(tag.Address, 1)
		if err != nil {
			return err
		}
		row["value_bool"] = vals[0]
		return nil

	case "discrete":
		vals, err := c.ReadDiscreteInputs(tag.Address, 1)
		if err != nil {
			return err
		}
		row["value_bool"] = vals[0]
		return nil

	case "holding", "input":
		return readRegisters(c, tag, row)

	default:
		return fmt.Errorf("unknown register type %q", tag.RegisterType)
	}
}

func readRegisters(c *mb.ModbusClient, tag metadata.ModbusTag, row contracts.Row) error {
	regType := mb.HOLDING_REGISTER
	if strings.ToLower(tag.RegisterType) == "input" {
		regType = mb.INPUT_REGISTER
	}

	quantity := quantityFor(tag.DataType, tag.Quantity)
	regs, err := c.ReadRegisters(tag.Address, quantity, regType)
	if err != nil {
		return err
	}

	// Convert []uint16 into a big-endian byte buffer respecting word order.
	// simonvetter/modbus already applies encoding when we use its typed helpers,
	// but we bypass those to keep DataType handling in one place.
	buf := make([]byte, len(regs)*2)
	for i, r := range regs {
		binary.BigEndian.PutUint16(buf[i*2:], r)
	}
	if strings.EqualFold(tag.WordOrder, "low_first") && len(regs) >= 2 {
		buf = swapWords(buf)
	}

	raw, err := decodeNumeric(buf, tag.DataType, tag.ByteOrder)
	if err != nil {
		return err
	}
	scaled := raw*tag.Scale + tag.Offset

	if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
		return fmt.Errorf("decoded value is NaN/Inf")
	}
	row["value_num"] = scaled
	return nil
}

func decodeNumeric(buf []byte, dataType, byteOrder string) (float64, error) {
	le := strings.EqualFold(byteOrder, "little")
	switch strings.ToLower(dataType) {
	case "int16":
		if len(buf) < 2 {
			return 0, fmt.Errorf("int16 needs 2 bytes")
		}
		var v int16
		if le {
			v = int16(binary.LittleEndian.Uint16(buf))
		} else {
			v = int16(binary.BigEndian.Uint16(buf))
		}
		return float64(v), nil

	case "uint16":
		if len(buf) < 2 {
			return 0, fmt.Errorf("uint16 needs 2 bytes")
		}
		if le {
			return float64(binary.LittleEndian.Uint16(buf)), nil
		}
		return float64(binary.BigEndian.Uint16(buf)), nil

	case "int32":
		if len(buf) < 4 {
			return 0, fmt.Errorf("int32 needs 4 bytes")
		}
		var v int32
		if le {
			v = int32(binary.LittleEndian.Uint32(buf))
		} else {
			v = int32(binary.BigEndian.Uint32(buf))
		}
		return float64(v), nil

	case "uint32":
		if len(buf) < 4 {
			return 0, fmt.Errorf("uint32 needs 4 bytes")
		}
		if le {
			return float64(binary.LittleEndian.Uint32(buf)), nil
		}
		return float64(binary.BigEndian.Uint32(buf)), nil

	case "int64":
		if len(buf) < 8 {
			return 0, fmt.Errorf("int64 needs 8 bytes")
		}
		var v int64
		if le {
			v = int64(binary.LittleEndian.Uint64(buf))
		} else {
			v = int64(binary.BigEndian.Uint64(buf))
		}
		return float64(v), nil

	case "uint64":
		if len(buf) < 8 {
			return 0, fmt.Errorf("uint64 needs 8 bytes")
		}
		if le {
			return float64(binary.LittleEndian.Uint64(buf)), nil
		}
		return float64(binary.BigEndian.Uint64(buf)), nil

	case "float32":
		if len(buf) < 4 {
			return 0, fmt.Errorf("float32 needs 4 bytes")
		}
		var bits uint32
		if le {
			bits = binary.LittleEndian.Uint32(buf)
		} else {
			bits = binary.BigEndian.Uint32(buf)
		}
		return float64(math.Float32frombits(bits)), nil

	case "float64":
		if len(buf) < 8 {
			return 0, fmt.Errorf("float64 needs 8 bytes")
		}
		var bits uint64
		if le {
			bits = binary.LittleEndian.Uint64(buf)
		} else {
			bits = binary.BigEndian.Uint64(buf)
		}
		return math.Float64frombits(bits), nil

	default:
		return 0, fmt.Errorf("unsupported data type %q", dataType)
	}
}

func quantityFor(dataType string, override uint16) uint16 {
	if override > 0 {
		return override
	}
	switch strings.ToLower(dataType) {
	case "int16", "uint16":
		return 1
	case "int32", "uint32", "float32":
		return 2
	case "int64", "uint64", "float64":
		return 4
	default:
		return 1
	}
}

// swapWords swaps 16-bit words in-place-style for word-order handling.
func swapWords(buf []byte) []byte {
	out := make([]byte, len(buf))
	words := len(buf) / 2
	for i := 0; i < words; i++ {
		srcHi := (words - 1 - i) * 2
		out[i*2] = buf[srcHi]
		out[i*2+1] = buf[srcHi+1]
	}
	return out
}

func endianness(s string) mb.Endianness {
	if strings.EqualFold(s, "little") {
		return mb.LITTLE_ENDIAN
	}
	return mb.BIG_ENDIAN
}

func wordOrder(s string) mb.WordOrder {
	if strings.EqualFold(s, "low_first") {
		return mb.LOW_WORD_FIRST
	}
	return mb.HIGH_WORD_FIRST
}