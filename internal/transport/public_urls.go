package transport

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerPublicURLTool(ms *server.MCPServer) {
	tool := mcp.NewToolWithRawSchema("get_public_urls", "Get the public customer membership registration URL when asked for a public URL or registration link. Uses CRM_BASE_URL as the service base URL, including any MCP instance prefix. If unset, returns https://centermcp.com/<mcp-id>/public/register as a template; replace <mcp-id> with the actual MCP instance ID before sharing.", json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`))
	mcp.WithReadOnlyHintAnnotation(true)(&tool)
	mcp.WithDestructiveHintAnnotation(false)(&tool)
	ms.AddTool(tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		base := strings.TrimSpace(os.Getenv("CRM_BASE_URL"))
		isTemplate := base == ""
		message := "Share the registration URL with customers."
		if isTemplate {
			base = "https://centermcp.com/<mcp-id>"
			message = "Replace <mcp-id> with the actual MCP instance ID before sharing, or configure CRM_BASE_URL."
		} else {
			u, err := url.Parse(base)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(base, "#") {
				return mcp.NewToolResultError("CRM_BASE_URL must be an absolute HTTP or HTTPS service URL without credentials, a query, or a fragment. Include any MCP instance prefix, but omit /mcp and /public/register."), nil
			}
		}
		result := map[string]any{
			"registration_url": strings.TrimRight(base, "/") + "/public/register",
			"is_template":      isTemplate,
			"message":          message,
		}
		body, _ := json.Marshal(result)
		return mcp.NewToolResultStructured(result, string(body)), nil
	})
}
