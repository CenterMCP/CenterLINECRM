package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/center/CenterLINECRM/internal/crm"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerBulkTools(ms *server.MCPServer, s *crm.Store) {
	schemas := []struct{ name, description, schema string }{
		{"bulk_import_members", "Import 1–500 members from CDP or another source atomically. Match by normalized email or source + external_id. CRM IDs are generated. Existing names/email are updated; omitted active preserves existing status (new members default active). Balances and existing member access are preserved. Use get_member_coupon_url to return a personal coupon link to each member. Repeating an import updates the same members without duplicates; any failing row rolls back the batch.", `{"type":"object","properties":{"source":{"type":"string","minLength":1,"maxLength":80},"members":{"type":"array","minItems":1,"maxItems":500,"items":{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":200},"email":{"type":"string","minLength":3,"maxLength":254},"external_id":{"type":"string","minLength":1,"maxLength":120},"active":{"type":"boolean"}},"required":["name","email"],"additionalProperties":false}}},"required":["source","members"],"additionalProperties":false}`},
		{"bulk_adjust_points", "Award/remove points for 1–500 members atomically. Each row selects exactly one customer_id, email, or external_id; external_id requires batch source from bulk_import_members. Each row requires nonzero delta, reason and a stable reference unique per member. Identical retries do not apply points again; changed retries conflict. Negative balances and adjustments to inactive members are rejected. Any failed row rolls back the entire batch.", `{"type":"object","properties":{"source":{"type":"string","minLength":1,"maxLength":80},"adjustments":{"type":"array","minItems":1,"maxItems":500,"items":{"type":"object","properties":{"customer_id":{"type":"string","minLength":1,"maxLength":120},"email":{"type":"string","minLength":3,"maxLength":254},"external_id":{"type":"string","minLength":1,"maxLength":120},"delta":{"type":"integer","minimum":-1000000000,"maximum":1000000000},"reason":{"type":"string","minLength":1,"maxLength":500},"reference":{"type":"string","minLength":1,"maxLength":120}},"required":["delta","reason","reference"],"oneOf":[{"required":["customer_id"]},{"required":["email"]},{"required":["external_id"]}],"additionalProperties":false}}},"required":["adjustments"],"additionalProperties":false}`},
	}
	for _, spec := range schemas {
		tool := mcp.NewToolWithRawSchema(spec.name, spec.description, json.RawMessage(spec.schema))
		mcp.WithReadOnlyHintAnnotation(false)(&tool)
		mcp.WithDestructiveHintAnnotation(false)(&tool)
		name := spec.name
		ms.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in struct {
				Source      string                `json:"source"`
				Members     []crm.ImportMember    `json:"members"`
				Adjustments []crm.PointAdjustment `json:"adjustments"`
			}
			raw, err := json.Marshal(req.Params.Arguments)
			if err == nil {
				err = json.Unmarshal(raw, &in)
			}
			if err != nil {
				return mcp.NewToolResultError("invalid arguments"), nil
			}
			result := map[string]any{"tool": name}
			if name == "bulk_import_members" {
				var members []crm.ImportedMember
				members, err = s.BulkImportMembers(ctx, in.Source, in.Members)
				result["members"] = members
				created := 0
				for _, m := range members {
					if m.Created {
						created++
					}
				}
				result["created"] = created
				result["updated"] = len(members) - created
			} else {
				var entries []crm.Entry
				entries, err = s.BulkAdjustPoints(ctx, in.Source, in.Adjustments)
				result["entries"] = entries
				result["count"] = len(entries)
			}
			if err != nil {
				message := publicError(err)
				var batch *crm.BatchError
				if errors.As(err, &batch) {
					message = fmt.Sprintf("row %d: %s; no rows were committed", batch.Row, message)
				}
				return mcp.NewToolResultError(message), nil
			}
			body, _ := json.Marshal(result)
			return mcp.NewToolResultStructured(result, string(body)), nil
		})
	}
}
