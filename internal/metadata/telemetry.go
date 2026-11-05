package metadata

import (
	"context"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

// Ensure Repository implements contracts.TelemetryCatalog.
var _ contracts.TelemetryCatalog = (*Repository)(nil)

// ListModbusTags maps metadata rows into contracts.ModbusPoint values.
func (r *Repository) ListModbusTags(ctx context.Context, loadID int64) ([]contracts.ModbusPoint, error) {
	tags, err := r.listModbusTagRows(ctx, loadID)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.ModbusPoint, 0, len(tags))
	for _, t := range tags {
		out = append(out, contracts.ModbusPoint{
			CorrelationID: t.CorrelationID,
			TagName:       t.TagName,
			UnitID:        t.UnitID,
			RegisterType:  t.RegisterType,
			Address:       t.Address,
			Quantity:      t.Quantity,
			DataType:      t.DataType,
			ByteOrder:     t.ByteOrder,
			WordOrder:     t.WordOrder,
			Scale:         t.Scale,
			Offset:        t.Offset,
		})
	}
	return out, nil
}

// ListOPCUANodes maps metadata rows into contracts.OPCUAPoint values.
func (r *Repository) ListOPCUANodes(ctx context.Context, loadID int64) ([]contracts.OPCUAPoint, error) {
	nodes, err := r.listOPCUANodeRows(ctx, loadID)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.OPCUAPoint, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, contracts.OPCUAPoint{
			TagName: n.TagName,
			NodeID:  n.NodeID,
			Scale:   n.Scale,
			Offset:  n.Offset,
		})
	}
	return out, nil
}

func (r *Repository) listModbusTagRows(ctx context.Context, loadID int64) ([]ModbusTag, error) {
	var tags []ModbusTag
	err := r.db.WithContext(ctx).
		Where("load_id = ? AND is_enabled = ?", loadID, true).
		Order("id ASC").
		Find(&tags).Error
	return tags, err
}

func (r *Repository) listOPCUANodeRows(ctx context.Context, loadID int64) ([]OPCUANode, error) {
	var nodes []OPCUANode
	err := r.db.WithContext(ctx).
		Where("load_id = ? AND is_enabled = ?", loadID, true).
		Order("id ASC").
		Find(&nodes).Error
	return nodes, err
}
