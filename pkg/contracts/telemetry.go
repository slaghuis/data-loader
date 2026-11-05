package contracts

import "context"

// TelemetryCatalog supplies Modbus tags and OPC UA nodes for a load.
// It replaces the former package-level repository globals (D-14).
type TelemetryCatalog interface {
	ListModbusTags(ctx context.Context, loadID int64) ([]ModbusPoint, error)
	ListOPCUANodes(ctx context.Context, loadID int64) ([]OPCUAPoint, error)
}

// ModbusPoint is one register mapping for a Modbus load.
type ModbusPoint struct {
	CorrelationID string
	TagName       string
	UnitID        uint8
	RegisterType  string
	Address       uint16
	Quantity      uint16
	DataType      string
	ByteOrder     string
	WordOrder     string
	Scale         float64
	Offset        float64
}

// OPCUAPoint is one node mapping for an OPC UA load.
type OPCUAPoint struct {
	TagName string
	NodeID  string
	Scale   float64
	Offset  float64
}
