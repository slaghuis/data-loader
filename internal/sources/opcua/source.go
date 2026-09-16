package opcua

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	uac "github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"

	"github.com/slaghuis/data-loader/internal/logging"
	"github.com/slaghuis/data-loader/internal/metadata"
	"github.com/slaghuis/data-loader/internal/sources"
	"github.com/slaghuis/data-loader/pkg/contracts"
)

const kind = "opcua"

// NodeRepository is the minimum contract this module needs from the metadata repo.
type NodeRepository interface {
	ListOPCUANodes(ctx context.Context, loadID int64) ([]metadata.OPCUANode, error)
}

var nodeRepo NodeRepository

// SetNodeRepository is called once at startup from main.go.
func SetNodeRepository(r NodeRepository) { nodeRepo = r }

func init() {
	sources.Register(kind, New)
}

type Source struct {
	cfg    contracts.SourceConfig
	client *uac.Client
	mu     sync.Mutex
}

func New(cfg contracts.SourceConfig) (contracts.Source, error) {
	return &Source{cfg: cfg}, nil
}

func (s *Source) Kind() string { return kind }

func (s *Source) Open(ctx context.Context) error {
	endpoint := s.cfg.Params["endpoint"]
	if endpoint == "" {
		return fmt.Errorf("opcua source requires 'endpoint' param, e.g. opc.tcp://host:4840")
	}

	opts, err := buildClientOptions(s.cfg.Params)
	if err != nil {
		return fmt.Errorf("build opcua options: %w", err)
	}

	client, err := uac.NewClient(endpoint, opts...)
	if err != nil {
		return fmt.Errorf("new opcua client: %w", err)
	}
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect opcua: %w", err)
	}
	s.client = client
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	if s.client == nil {
		return fmt.Errorf("source not open")
	}
	// Read the server status node — a lightweight, always-present node.
	statusNode := ua.NewNumericNodeID(0, 2259) // Server_ServerStatus_State
	_, err := s.client.Read(ctx, &ua.ReadRequest{
		NodesToRead: []*ua.ReadValueID{
			{NodeID: statusNode, AttributeID: ua.AttributeIDValue},
		},
	})
	return err
}

func (s *Source) Close() error {
	if s.client != nil {
		return s.client.Close(context.Background())
	}
	return nil
}

// Read performs one polling cycle: batch-read all enabled nodes.
func (s *Source) Read(ctx context.Context, load contracts.LoadConfig, handler contracts.BatchHandler) (contracts.ReadResult, error) {
	log := logging.FromContext(ctx)
	start := time.Now().UTC()
	result := contracts.ReadResult{StartedAt: start}

	if nodeRepo == nil {
		return result, fmt.Errorf("opcua node repository not configured")
	}

	nodes, err := nodeRepo.ListOPCUANodes(ctx, load.LoadID)
	if err != nil {
		return result, fmt.Errorf("load nodes: %w", err)
	}
	if len(nodes) == 0 {
		log.Warn("no enabled nodes for opcua load", "category", "read")
		result.FinishedAt = time.Now().UTC()
		return result, nil
	}

	columns := []contracts.Column{
		{Name: "tag_name", DataType: "string", Nullable: false},
		{Name: "node_id", DataType: "string", Nullable: false},
		{Name: "ts", DataType: "datetime", Nullable: false},
		{Name: "value_num", DataType: "float64", Nullable: true},
		{Name: "value_bool", DataType: "bool", Nullable: true},
		{Name: "value_text", DataType: "string", Nullable: true},
		{Name: "quality", DataType: "string", Nullable: false},
	}

	// Build the batch read request.
	nodesToRead := make([]*ua.ReadValueID, 0, len(nodes))
	parsedIDs := make([]*ua.NodeID, 0, len(nodes))
	for _, n := range nodes {
		nid, err := ua.ParseNodeID(n.NodeID)
		if err != nil {
			// One bad node id shouldn't break the whole batch — but we still need
			// the slot indexes to line up, so we emit an error row later.
			parsedIDs = append(parsedIDs, nil)
			continue
		}
		parsedIDs = append(parsedIDs, nid)
		nodesToRead = append(nodesToRead, &ua.ReadValueID{
			NodeID:      nid,
			AttributeID: ua.AttributeIDValue,
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	pollTS := time.Now().UTC()
	rows := make([]contracts.Row, 0, len(nodes))

	// Execute batch read if we have any valid node IDs.
	var resp *ua.ReadResponse
	if len(nodesToRead) > 0 {
		req := &ua.ReadRequest{
			MaxAge:             0,
			TimestampsToReturn: ua.TimestampsToReturnBoth,
			NodesToRead:        nodesToRead,
		}
		resp, err = s.client.Read(ctx, req)
		if err != nil {
			// Total read failure — emit "bad" rows for every node so downstream can see the gap.
			log.Error("opcua batch read failed", "category", "read", "err", err)
			for _, n := range nodes {
				rows = append(rows, badRow(n, pollTS, err.Error()))
				result.RowsRead++
			}
			return emit(ctx, handler, contracts.Batch{Columns: columns, Rows: rows}, &result)
		}
	}

	// Walk nodes and response in lockstep. parsedIDs preserves index alignment
	// with `nodes`; nodesToRead skipped the nils.
	respIdx := 0
	for i, n := range nodes {
		if parsedIDs[i] == nil {
			rows = append(rows, badRow(n, pollTS, "invalid node id"))
			result.RowsRead++
			continue
		}
		if resp == nil || respIdx >= len(resp.Results) {
			rows = append(rows, badRow(n, pollTS, "no response for node"))
			result.RowsRead++
			respIdx++
			continue
		}
		dv := resp.Results[respIdx]
		respIdx++

		row := contracts.Row{
			"tag_name":   n.TagName,
			"node_id":    n.NodeID,
			"ts":         pickTimestamp(dv, pollTS),
			"value_num":  nil,
			"value_bool": nil,
			"value_text": nil,
			"quality":    qualityFromStatus(dv.Status),
		}

		if dv.Status != ua.StatusOK {
			row["value_text"] = dv.Status.Error()
		} else if dv.Value != nil {
			assignValue(row, dv.Value.Value(), n.Scale, n.Offset)
		} else {
			row["quality"] = "bad"
			row["value_text"] = "null value"
		}
		rows = append(rows, row)
		result.RowsRead++
	}

	return emit(ctx, handler, contracts.Batch{Columns: columns, Rows: rows}, &result)
}

// ---- helpers ----

func emit(ctx context.Context, handler contracts.BatchHandler, batch contracts.Batch, result *contracts.ReadResult) (contracts.ReadResult, error) {
	if err := handler(ctx, batch); err != nil {
		return *result, err
	}
	result.FinishedAt = time.Now().UTC()
	return *result, nil
}

func badRow(n metadata.OPCUANode, ts time.Time, msg string) contracts.Row {
	return contracts.Row{
		"tag_name":   n.TagName,
		"node_id":    n.NodeID,
		"ts":         ts,
		"value_num":  nil,
		"value_bool": nil,
		"value_text": msg,
		"quality":    "bad",
	}
}

func qualityFromStatus(s ua.StatusCode) string {
	switch {
	case s == ua.StatusOK:
		return "good"
	case s.Has(ua.StatusBad):
		return "bad"
	case s.Has(ua.StatusUncertain):
		return "uncertain"
	default:
		return "good"
	}
}

func pickTimestamp(dv *ua.DataValue, fallback time.Time) time.Time {
	if dv == nil {
		return fallback
	}
	if !dv.SourceTimestamp.IsZero() {
		return dv.SourceTimestamp.UTC()
	}
	if !dv.ServerTimestamp.IsZero() {
		return dv.ServerTimestamp.UTC()
	}
	return fallback
}

// assignValue populates the correct sink column based on Go value type.
// Applies scale/offset only to numeric types.
func assignValue(row contracts.Row, v any, scale, offset float64) {
	if v == nil {
		row["quality"] = "bad"
		row["value_text"] = "null value"
		return
	}
	switch x := v.(type) {
	case bool:
		row["value_bool"] = x

	case string:
		row["value_text"] = x

	case float32:
		row["value_num"] = float64(x)*scale + offset
	case float64:
		row["value_num"] = x*scale + offset

	case int8:
		row["value_num"] = float64(x)*scale + offset
	case int16:
		row["value_num"] = float64(x)*scale + offset
	case int32:
		row["value_num"] = float64(x)*scale + offset
	case int64:
		row["value_num"] = float64(x)*scale + offset

	case uint8:
		row["value_num"] = float64(x)*scale + offset
	case uint16:
		row["value_num"] = float64(x)*scale + offset
	case uint32:
		row["value_num"] = float64(x)*scale + offset
	case uint64:
		row["value_num"] = float64(x)*scale + offset

	case time.Time:
		row["value_text"] = x.UTC().Format(time.RFC3339Nano)

	default:
		// Arrays, structures, ExtensionObjects — represent as string for now.
		row["value_text"] = fmt.Sprintf("%v", v)
	}
}

// buildClientOptions translates params into gopcua client options.
//
// Recognized params:
//   endpoint          (required) e.g. opc.tcp://plc.mine.local:4840
//   security_policy   None | Basic256Sha256 | ...     (default: None)
//   security_mode     None | Sign | SignAndEncrypt    (default: None)
//   auth              anonymous | username            (default: anonymous)
//   username, password (if auth=username)
//   session_timeout_ms (default 60000)
//   request_timeout_ms (default 10000)
func buildClientOptions(p map[string]string) ([]uac.Option, error) {
	opts := []uac.Option{}

	policy := valueOr(p, "security_policy", "None")
	mode := valueOr(p, "security_mode", "None")
	opts = append(opts, uac.SecurityPolicy(policyURI(policy)))
	opts = append(opts, uac.SecurityModeString(mode))

	switch strings.ToLower(valueOr(p, "auth", "anonymous")) {
	case "anonymous":
		opts = append(opts, uac.AuthAnonymous())
	case "username":
		user := p["username"]
		pass := p["password"]
		if user == "" {
			return nil, fmt.Errorf("auth=username requires 'username' param")
		}
		opts = append(opts, uac.AuthUsername(user, pass))
	default:
		return nil, fmt.Errorf("unsupported auth type %q", p["auth"])
	}

	if v, ok := p["session_timeout_ms"]; ok && v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			opts = append(opts, uac.SessionTimeout(time.Duration(ms)*time.Millisecond))
		}
	}
	if v, ok := p["request_timeout_ms"]; ok && v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			opts = append(opts, uac.RequestTimeout(time.Duration(ms)*time.Millisecond))
		}
	}

	return opts, nil
}

// policyURI maps the short policy name to the full OPC-UA URI.
func policyURI(short string) string {
	switch strings.ToLower(short) {
	case "none":
		return "http://opcfoundation.org/UA/SecurityPolicy#None"
	case "basic128rsa15":
		return "http://opcfoundation.org/UA/SecurityPolicy#Basic128Rsa15"
	case "basic256":
		return "http://opcfoundation.org/UA/SecurityPolicy#Basic256"
	case "basic256sha256":
		return "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256"
	case "aes128_sha256_rsaoaep":
		return "http://opcfoundation.org/UA/SecurityPolicy#Aes128_Sha256_RsaOaep"
	case "aes256_sha256_rsapss":
		return "http://opcfoundation.org/UA/SecurityPolicy#Aes256_Sha256_RsaPss"
	default:
		return "http://opcfoundation.org/UA/SecurityPolicy#None"
	}
}

func valueOr(m map[string]string, k, def string) string {
	if v, ok := m[k]; ok && v != "" {
		return v
	}
	return def
}