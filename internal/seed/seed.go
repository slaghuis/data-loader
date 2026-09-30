package seed

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"

    "gorm.io/gorm"

    "github.com/slaghuis/data-loader/internal/metadata"
)

// Apply reconciles the seed file against the metadata store.
// - Sources and loads are upserted by name.
// - Modbus tags and OPC UA nodes are replaced wholesale for each load.
// - Nothing is deleted at the source/load level.
func Apply(ctx context.Context, db *gorm.DB, f *File) (Report, error) {
    var rep Report

    // Group tags by load name up front so we can replace-all per load.
    modbusByLoad := make(map[string][]ModbusTagSpec)
    for _, t := range f.ModbusTags {
        if t.Load == "" {
            return rep, fmt.Errorf("modbus_tag %q missing load reference", t.TagName)
        }
        modbusByLoad[t.Load] = append(modbusByLoad[t.Load], t)
    }
    opcuaByLoad := make(map[string][]OPCUANodeSpec)
    for _, n := range f.OPCUANodes {
        if n.Load == "" {
            return rep, fmt.Errorf("opcua_node %q missing load reference", n.TagName)
        }
        opcuaByLoad[n.Load] = append(opcuaByLoad[n.Load], n)
    }

    // Track loads declared in this file so we know which tag groups are valid.
    declaredLoads := make(map[string]bool, len(f.Loads))
    for _, l := range f.Loads {
        declaredLoads[l.Name] = true
    }
    for loadName := range modbusByLoad {
        if !declaredLoads[loadName] {
            return rep, fmt.Errorf("modbus_tags reference undeclared load %q", loadName)
        }
    }
    for loadName := range opcuaByLoad {
        if !declaredLoads[loadName] {
            return rep, fmt.Errorf("opcua_nodes reference undeclared load %q", loadName)
        }
    }

    err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        srcByName := make(map[string]*metadata.Source, len(f.Sources))
        for _, spec := range f.Sources {
            s, action, err := upsertSource(tx, spec)
            if err != nil {
                return fmt.Errorf("source %q: %w", spec.Name, err)
            }
            srcByName[spec.Name] = s
            rep.recordSource(action, spec.Name)
        }

        for _, spec := range f.Loads {
            src, ok := srcByName[spec.Source]
            if !ok {
                var existing metadata.Source
                if err := tx.First(&existing, "name = ?", spec.Source).Error; err != nil {
                    if errors.Is(err, gorm.ErrRecordNotFound) {
                        return fmt.Errorf("load %q references unknown source %q", spec.Name, spec.Source)
                    }
                    return err
                }
                src = &existing
            }

            l, action, err := upsertLoad(tx, spec, src.ID)
            if err != nil {
                return fmt.Errorf("load %q: %w", spec.Name, err)
            }
            rep.recordLoad(action, spec.Name)

            if tags, ok := modbusByLoad[spec.Name]; ok {
                n, err := replaceModbusTags(tx, l.ID, tags)
                if err != nil {
                    return fmt.Errorf("load %q modbus tags: %w", spec.Name, err)
                }
                rep.ModbusTags += n
            }
            if nodes, ok := opcuaByLoad[spec.Name]; ok {
                n, err := replaceOPCUANodes(tx, l.ID, nodes)
                if err != nil {
                    return fmt.Errorf("load %q opcua nodes: %w", spec.Name, err)
                }
                rep.OPCUANodes += n
            }
        }
        return nil
    })
    return rep, err
}

// ---- upserts ----

func upsertSource(tx *gorm.DB, spec SourceSpec) (*metadata.Source, Action, error) {
    if spec.Name == "" || spec.Kind == "" {
        return nil, "", fmt.Errorf("name and kind are required")
    }
    paramsJSON, err := json.Marshal(spec.Params)
    if err != nil {
        return nil, "", fmt.Errorf("marshal params: %w", err)
    }

    var s metadata.Source
    err = tx.First(&s, "name = ?", spec.Name).Error
    switch {
    case errors.Is(err, gorm.ErrRecordNotFound):
        s = metadata.Source{
            Name:       spec.Name,
            Kind:       spec.Kind,
	    Description: spec.Description,
            ParamsJSON: string(paramsJSON),
            IsEnabled:  boolOr(spec.IsEnabled, true),
        }
        if err := tx.Create(&s).Error; err != nil {
            return nil, "", err
        }
        return &s, ActionCreated, nil
    case err != nil:
        return nil, "", err
    default:
        s.Kind = spec.Kind
	s.Description = spec.Description
        s.ParamsJSON = string(paramsJSON)
        s.IsEnabled = boolOr(spec.IsEnabled, s.IsEnabled)
        if err := tx.Save(&s).Error; err != nil {
            return nil, "", err
        }
        return &s, ActionUpdated, nil
    }
}

func upsertLoad(tx *gorm.DB, spec LoadSpec, sourceID int64) (*metadata.Load, Action, error) {
    if spec.Name == "" {
        return nil, "", fmt.Errorf("name is required")
    }
    var l metadata.Load
    err := tx.First(&l, "name = ?", spec.Name).Error
    switch {
    case errors.Is(err, gorm.ErrRecordNotFound):
        l = metadata.Load{
            Name:            spec.Name,
            SourceID:        sourceID,
            CronExpression:  spec.CronExpression,
            Mode:            spec.Mode,
            ObjectName:      spec.ObjectName,
            WatermarkColumn: spec.WatermarkColumn,
            WatermarkType:   spec.WatermarkType,
            TargetSchema:    spec.TargetSchema,
            TargetTable:     spec.TargetTable,
            BatchSize:       spec.BatchSize,
            AutoCreateTable: boolOr(spec.AutoCreateTable, true),
            IsEnabled:       boolOr(spec.IsEnabled, true),
        }
        if err := tx.Create(&l).Error; err != nil {
            return nil, "", err
        }
        return &l, ActionCreated, nil
    case err != nil:
        return nil, "", err
    default:
        l.SourceID = sourceID
        l.CronExpression = spec.CronExpression
        l.Mode = spec.Mode
        l.ObjectName = spec.ObjectName
        l.WatermarkColumn = spec.WatermarkColumn
        l.WatermarkType = spec.WatermarkType
        l.TargetSchema = spec.TargetSchema
        l.TargetTable = spec.TargetTable
        l.BatchSize = spec.BatchSize
        l.AutoCreateTable = boolOr(spec.AutoCreateTable, l.AutoCreateTable)
        l.IsEnabled = boolOr(spec.IsEnabled, l.IsEnabled)
        if err := tx.Save(&l).Error; err != nil {
            return nil, "", err
        }
        return &l, ActionUpdated, nil
    }
}

func replaceModbusTags(tx *gorm.DB, loadID int64, specs []ModbusTagSpec) (int, error) {
    if err := tx.Where("load_id = ?", loadID).Delete(&metadata.ModbusTag{}).Error; err != nil {
        return 0, err
    }
    rows := make([]metadata.ModbusTag, 0, len(specs))
    for _, t := range specs {
        if t.TagName == "" {
            return 0, fmt.Errorf("modbus tag requires tag_name")
        }
        if t.RegisterType == "" {
            return 0, fmt.Errorf("modbus tag %q requires register_type", t.TagName)
        }
        if t.DataType == "" {
            return 0, fmt.Errorf("modbus tag %q requires data_type", t.TagName)
        }

        rows = append(rows, metadata.ModbusTag{
            LoadID:        loadID,
            TagName:       t.TagName,
            CorrelationID: t.CorrelationID,             // nullable in DB
            UnitID:        t.UnitID,
            RegisterType:  t.RegisterType,
            Address:       t.Address,
            Quantity:      defaultQuantity(t),          // sensible per data_type
            DataType:      t.DataType,
            ByteOrder:     stringOr(t.ByteOrder, "big"),
            WordOrder:     stringOr(t.WordOrder, "high_first"),
            Scale:         float64Or(t.Scale, 1.0),
            Offset:        float64Or(t.Offset, 0.0),
            IsEnabled:     boolOr(t.IsEnabled, true),
        })
    }
    if len(rows) == 0 {
        return 0, nil
    }
    if err := tx.Create(&rows).Error; err != nil {
        return 0, err
    }
    return len(rows), nil
}

func replaceOPCUANodes(tx *gorm.DB, loadID int64, specs []OPCUANodeSpec) (int, error) {
    if err := tx.Where("load_id = ?", loadID).Delete(&metadata.OPCUANode{}).Error; err != nil {
        return 0, err
    }
    rows := make([]metadata.OPCUANode, 0, len(specs))
    for _, n := range specs {
        if n.TagName == "" || n.NodeID == "" {
            return 0, fmt.Errorf("opcua node requires tag_name and node_id")
        }
        rows = append(rows, metadata.OPCUANode{
            LoadID:    loadID,
            TagName:   n.TagName,
            NodeID:    n.NodeID,
            Scale:     float64Or(n.Scale, 1.0),
            Offset:    float64Or(n.Offset, 0.0),
            IsEnabled: boolOr(n.IsEnabled, true),
        })
    }
    if len(rows) == 0 {
        return 0, nil
    }
    if err := tx.Create(&rows).Error; err != nil {
        return 0, err
    }
    return len(rows), nil
}

// ---- reporting ----

type Action string

const (
    ActionCreated Action = "created"
    ActionUpdated Action = "updated"
)

type Report struct {
    SourcesCreated []string
    SourcesUpdated []string
    LoadsCreated   []string
    LoadsUpdated   []string
    ModbusTags     int
    OPCUANodes     int
}

func (r *Report) recordSource(a Action, name string) {
    if a == ActionCreated {
        r.SourcesCreated = append(r.SourcesCreated, name)
    } else {
        r.SourcesUpdated = append(r.SourcesUpdated, name)
    }
}

func (r *Report) recordLoad(a Action, name string) {
    if a == ActionCreated {
        r.LoadsCreated = append(r.LoadsCreated, name)
    } else {
        r.LoadsUpdated = append(r.LoadsUpdated, name)
    }
}

// ---- helpers ----

func boolOr(p *bool, def bool) bool {
    if p == nil {
        return def
    }
    return *p
}

func stringOr(s, def string) string {
    if s == "" {
        return def
    }
    return s
}

func float64Or(p *float64, def float64) float64 {
    if p == nil {
        return def
    }
    return *p
}

// defaultQuantity picks a register count from data_type if not specified.
// Mirrors the runtime logic in modbus/source.go's quantityFor().
func defaultQuantity(t ModbusTagSpec) uint16 {
    if t.Quantity > 0 {
        return t.Quantity
    }
    switch strings.ToLower(t.DataType) {
    case "int16", "uint16", "bool":
        return 1
    case "int32", "uint32", "float32":
        return 2
    case "int64", "uint64", "float64":
        return 4
    default:
        return 1
    }
}
