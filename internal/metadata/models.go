package metadata

import (
	"time"

	"gorm.io/gorm"
)

// Source represents a data source instance (a specific server/gateway).
type Source struct {
	ID          int64          `gorm:"primaryKey;autoIncrement"`
	Name        string         `gorm:"size:200;uniqueIndex;not null"`
	Kind        string         `gorm:"size:50;not null;index"` // sqlserver, postgres, modbus, opcua
	Description string         `gorm:"size:500"`
	// Params is stored as JSON. Sensitive fields should be referenced by
	// secret name (resolved from env/secrets store at runtime), not stored raw.
	ParamsJSON  string         `gorm:"type:nvarchar(max);not null"`
	IsEnabled   bool           `gorm:"not null;default:1"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

func (Source) TableName() string { return "meta_sources" }

// Load represents a single data movement definition.
type Load struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	SourceID        int64  `gorm:"not null;index"`
	Source          Source `gorm:"foreignKey:SourceID"`

	Name            string `gorm:"size:200;uniqueIndex;not null"`
	ObjectName      string `gorm:"size:400;not null"` // source table/view or tag group

	// Sink target
	TargetSchema   string `gorm:"size:100;not null;default:dbo"`
	TargetTable    string `gorm:"size:200;not null"`

	// Load behavior
	Mode            string `gorm:"size:20;not null"` // delta | append | replace
	WatermarkColumn string `gorm:"size:200"`
	WatermarkType   string `gorm:"size:20;not null;default:none"` // none | datetime | numeric

	BatchSize       int    `gorm:"not null;default:1000"`
	AutoCreateTable bool   `gorm:"not null;default:1"`

	// Scheduling — cron expression (robfig/cron v3 format)
	CronExpression  string `gorm:"size:100;not null"`
	IsEnabled       bool   `gorm:"not null;default:1"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (Load) TableName() string { return "meta_loads" }

// Watermark stores the current watermark value for a load.
// One row per load. Updated after each successful run.
type Watermark struct {
	LoadID       int64     `gorm:"primaryKey"`
	Value        string    `gorm:"size:100;not null"`  // stored as string; typed via Load.WatermarkType
	UpdatedAt    time.Time
	PreviousValue string   `gorm:"size:100"`           // for auditing / rollback
	ResetAt      *time.Time
	ResetReason  string    `gorm:"size:500"`
}

func (Watermark) TableName() string { return "meta_watermarks" }

// LoadRun is one execution attempt of a load.
type LoadRun struct {
	ID               int64     `gorm:"primaryKey;autoIncrement"`
	RunUUID          string    `gorm:"size:36;uniqueIndex;not null"`
	LoadID           int64     `gorm:"not null;index"`
	Status           string    `gorm:"size:20;not null"` // running | success | failed | aborted
	StartedAt        time.Time `gorm:"not null;index"`
	FinishedAt       *time.Time
	RowsRead         int64
	RowsWritten      int64
	WatermarkStart   string    `gorm:"size:100"`
	WatermarkEnd     string    `gorm:"size:100"`
	ErrorMessage     string    `gorm:"type:nvarchar(max)"`
	DurationMs       int64
}

func (LoadRun) TableName() string { return "meta_load_runs" }

// LogEntry is a structured log line tied to a run (or standalone).
type LogEntry struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `gorm:"not null;index"`
	Level     string    `gorm:"size:10;not null;index"` // debug | info | warn | error
	RunUUID   string    `gorm:"size:36;index"`          // nullable
	LoadID    *int64    `gorm:"index"`
	SourceID  *int64    `gorm:"index"`
	Category  string    `gorm:"size:50;index"`          // connect, read, write, watermark, schedule
	Message   string    `gorm:"type:nvarchar(max);not null"`
	Details   string    `gorm:"type:nvarchar(max)"`     // JSON payload
}

func (LogEntry) TableName() string { return "meta_log_entries" }

// Modbus TCP metadata
// ModbusTag defines a single register mapping for a Modbus load.
type ModbusTag struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	LoadID       int64  `gorm:"not null;index"`

	TagName      string `gorm:"size:200;not null"`

	// Modbus addressing
	UnitID       uint8  `gorm:"not null;default:1"`     // slave/unit ID
	RegisterType string `gorm:"size:20;not null"`       // coil | discrete | holding | input
	Address      uint16 `gorm:"not null"`               // 0-based register address
	Quantity     uint16 `gorm:"not null;default:1"`     // 1 for bool/int16, 2 for int32/float32, 4 for float64

	// Decoding
	DataType     string  `gorm:"size:20;not null"`      // bool | int16 | uint16 | int32 | uint32 | int64 | uint64 | float32 | float64
	ByteOrder    string  `gorm:"size:10;not null;default:big"`     // big | little
	WordOrder    string  `gorm:"size:10;not null;default:high_first"` // high_first | low_first (for 32/64-bit values)

	// Scaling: value = (raw * Scale) + Offset
	Scale        float64 `gorm:"not null;default:1"`
	Offset       float64 `gorm:"not null;default:0"`

	IsEnabled    bool    `gorm:"not null;default:1"`
}

func (ModbusTag) TableName() string { return "meta_modbus_tags" }

// OPC-UA Source Loader
// OPCUANode defines a single OPC-UA node to poll for a load.
type OPCUANode struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	LoadID      int64  `gorm:"not null;index"`

	TagName     string `gorm:"size:200;not null"`   // logical name (mine-friendly)
	NodeID      string `gorm:"size:400;not null"`   // OPC-UA node id, e.g. "ns=2;s=Channel1.Device1.Tag"

	// Scaling: value = (raw * Scale) + Offset — only applied to numeric values.
	Scale       float64 `gorm:"not null;default:1"`
	Offset      float64 `gorm:"not null;default:0"`

	IsEnabled   bool    `gorm:"not null;default:1"`
}

func (OPCUANode) TableName() string { return "meta_opcua_nodes" }
