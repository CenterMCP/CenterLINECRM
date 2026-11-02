package transport

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/center/CenterLINECRM/internal/crm"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const Version = "0.2.0"

const ResourceURI = "ui://centerlinecrm/admin/v1.html"

type input struct {
	Kind         string          `json:"kind"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Active       bool            `json:"active"`
	Data         json.RawMessage `json:"data"`
	Description  string          `json:"description"`
	Cost         int64           `json:"cost"`
	Stock        int64           `json:"stock"`
	CustomerID   string          `json:"customer_id"`
	Delta        int64           `json:"delta"`
	Reason       string          `json:"reason"`
	Reference    string          `json:"reference"`
	Page         int             `json:"page"`
	BusinessName string          `json:"business_name"`
	LogoURL      string          `json:"logo_url"`
	PrimaryColor string          `json:"primary_color"`
	Section      string          `json:"section"`
}

func MCP(s *crm.Store) *server.MCPServer {
	ms := server.NewMCPServer("CenterLINECRM", Version, server.WithResourceCapabilities(false, false), server.WithInputSchemaValidation())
	registerPublicURLTool(ms)
	resource := mcp.NewResource(ResourceURI, "centerlinecrm-admin", mcp.WithResourceTitle("CenterLINECRM master data"), mcp.WithMIMEType("text/html;profile=mcp-app"))
	ms.AddResource(resource, func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: ResourceURI, MIMEType: "text/html;profile=mcp-app", Text: adminHTML}}, nil
	})
	type spec struct {
		name, description string
		read              bool
		fields            map[string]string
		required          []string
	}
	specs := []spec{
		{"update_business_branding", "Set the business branding on the public registration and member CRM page. Supply business_name, an absolute HTTPS logo_url (empty to remove), and primary_color as #RRGGBB. All three fields are required. Changes appear on the next page load.", false, map[string]string{"business_name": "string", "logo_url": "string", "primary_color": "string"}, []string{"business_name", "logo_url", "primary_color"}},
		{"open_crm_admin", "Open the CRM editor. Choose master, privileges, customers, points, or redemptions; supply customer_id for customer-specific history.", true, map[string]string{"section": "string", "customer_id": "string"}, nil},
		{"save_master_record", "Create or update master data: tiers, branches, categories, program settings or other kinds. Use active=false to archive.", false, map[string]string{"kind": "string", "id": "string", "name": "string", "active": "boolean", "data": "object"}, []string{"kind", "id", "name", "active", "data"}},
		{"list_master_records", "List all master data, optionally filtered by kind. Pages contain at most 100 records.", true, map[string]string{"kind": "string", "page": "integer"}, nil},
		{"save_privilege", "Create or update a privilege. Cost is positive integer points. Stock is remaining units. Use active=false to archive.", false, map[string]string{"id": "string", "name": "string", "description": "string", "cost": "integer", "stock": "integer", "active": "boolean"}, []string{"id", "name", "description", "cost", "stock", "active"}},
		{"list_privileges", "List active and archived privileges.", true, map[string]string{"page": "integer"}, nil},
		{"list_customers", "List customer profiles and point balances.", true, map[string]string{"page": "integer"}, nil},
		{"update_customer", "Update a customer name and active status; balances must use adjust_points.", false, map[string]string{"id": "string", "name": "string", "active": "boolean"}, []string{"id", "name", "active"}},
		{"adjust_points", "Award or remove points with an audit reason and unique reference per customer. Identical retries are safe. No negative balances.", false, map[string]string{"customer_id": "string", "delta": "integer", "reason": "string", "reference": "string"}, []string{"customer_id", "delta", "reason", "reference"}},
		{"list_point_ledger", "List audited point transactions for a customer.", true, map[string]string{"customer_id": "string", "page": "integer"}, []string{"customer_id"}},
		{"list_redemptions", "List redemptions; optionally filter by customer.", true, map[string]string{"customer_id": "string", "page": "integer"}, nil},
	}
	for _, sp := range specs {
		props := map[string]any{}
		for k, t := range sp.fields {
			p := map[string]any{"type": t}
			if k == "page" {
				p["minimum"] = 1
				p["maximum"] = 1000000
			}
			if k == "section" {
				p["enum"] = []string{"master", "privileges", "customers", "points", "redemptions"}
			}
			if t == "string" {
				p["maxLength"] = 4000
			}
			if k == "cost" {
				p["minimum"] = 1
				p["maximum"] = 1000000000
			}
			if k == "stock" {
				p["minimum"] = 0
				p["maximum"] = 1000000000
			}
			if k == "delta" {
				p["minimum"] = -1000000000
				p["maximum"] = 1000000000
			}
			props[k] = p
		}
		required := sp.required
		if required == nil {
			required = []string{}
		}
		raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
		tool := mcp.NewToolWithRawSchema(sp.name, sp.description, raw)
		mcp.WithReadOnlyHintAnnotation(sp.read)(&tool)
		mcp.WithDestructiveHintAnnotation(!sp.read)(&tool)
		tool.Meta = mcp.NewMetaFromMap(map[string]any{"ui": map[string]any{"resourceUri": ResourceURI}, "openai/outputTemplate": ResourceURI})
		name := sp.name
		ms.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in input
			raw, err := json.Marshal(req.Params.Arguments)
			if err == nil {
				err = json.Unmarshal(raw, &in)
			}
			if err != nil {
				return mcp.NewToolResultError("Invalid arguments"), nil
			}
			if in.Page == 0 {
				in.Page = 1
			}
			if in.Page < 1 || in.Page > 1000000 {
				return mcp.NewToolResultError("Invalid page"), nil
			}
			offset := (in.Page - 1) * 100
			var value any
			switch name {
			case "update_business_branding":
				value, err = s.UpdateBusinessBranding(ctx, crm.BusinessBranding{BusinessName: in.BusinessName, LogoURL: in.LogoURL, PrimaryColor: in.PrimaryColor})
			case "open_crm_admin":
				value = map[string]any{"message": "Use the editor to manage master records and privileges. Archive records by setting active to false."}
			case "save_master_record":
				err = s.SaveRecord(ctx, crm.Record{Kind: in.Kind, ID: in.ID, Name: in.Name, Active: in.Active, Data: in.Data})
				value = map[string]any{"saved": in.ID}
			case "list_master_records":
				value, err = s.Records(ctx, in.Kind, offset)
			case "save_privilege":
				err = s.SavePrivilege(ctx, crm.Privilege{ID: in.ID, Name: in.Name, Description: in.Description, Cost: in.Cost, Stock: in.Stock, Active: in.Active})
				value = map[string]any{"saved": in.ID}
			case "list_privileges":
				value, err = s.Privileges(ctx, false, offset)
			case "list_customers":
				value, err = s.Customers(ctx, offset)
			case "update_customer":
				err = s.UpdateCustomer(ctx, in.ID, in.Name, in.Active)
				value = map[string]any{"saved": in.ID}
			case "adjust_points":
				value, err = s.Adjust(ctx, in.CustomerID, in.Delta, in.Reason, in.Reference)
			case "list_point_ledger":
				if in.CustomerID == "" {
					err = crm.ErrInvalid
				} else {
					value, err = s.History(ctx, in.CustomerID, offset)
				}
			case "list_redemptions":
				value, err = s.Redemptions(ctx, in.CustomerID, offset)
			default:
				err = fmt.Errorf("unknown tool")
			}
			if err != nil {
				return mcp.NewToolResultError(publicError(err)), nil
			}
			result := map[string]any{"tool": name, "page": in.Page, "page_size": 100, "arguments": req.Params.Arguments, "data": value}
			body, _ := json.Marshal(result)
			return mcp.NewToolResultStructured(result, string(body)), nil
		})
	}
	return ms
}
