# CenterLINECRM

A SQLite loyalty CRM built from CenterCDP's Go, Streamable HTTP MCP, and embedded MCP Apps patterns. An LLM can manage master data through typed MCP tools, while customers register, view points, browse privileges, and redeem them through a separate public JSON API.

## Run

Requires Go 1.26+, CGO and a C compiler (SQLite uses `mattn/go-sqlite3`).

```sh
export CRM_ADMIN_TOKEN="$(openssl rand -hex 32)"
go build -o build/centerlinecrm ./cmd/server
./build/centerlinecrm --database ./data/centerlinecrm.db --address 127.0.0.1:3100
```

The database directory and version-1 schema are created automatically. The server defaults to localhost, port 3100. Keep the database, `-wal` and `-shm` files together; use SQLite's online backup API or stop the server before copying the database. Existing databases are preserved on startup.

## Connect an MCP client

Use a client that supports Streamable HTTP and bearer headers. Configuration shape varies by client:

```json
{
  "mcpServers": {
    "centerlinecrm": {
      "url": "http://localhost:3100/mcp",
      "headers": { "Authorization": "Bearer <CRM_ADMIN_TOKEN>" }
    }
  }
}
```

Ask the LLM to open the CRM admin UI, create a Gold tier, add a coffee privilege costing 100 points, or award a customer's purchase points. The LLM runs in your MCP client; this server does not need an LLM API key.

`open_crm_admin` accepts an optional `section` (`master`, `privileges`, `customers`, `points`, or `redemptions`) and `customer_id`. For example, `{"section":"points","customer_id":"<id>"}` opens that customer’s point ledger. LLM tool results automatically select the matching tab, preserve filters, and populate editor fields. Tool arguments are validated on the server, including required fields and unknown properties.

`open_crm_admin` exposes an embedded MCP App at `ui://centerlinecrm/admin/v1.html`. Compatible clients render tabs, editable forms, tables, and pagination. Every edit calls the same authenticated MCP tools. Clients without MCP Apps can use all tools directly.

| Tool | Purpose |
| --- | --- |
| `open_crm_admin` | Open the admin editor |
| `save_master_record` | Create/update/archive arbitrary master records |
| `list_master_records` | List master records, optionally by kind |
| `save_privilege` | Create/update/archive a redeemable privilege |
| `list_privileges` | List active and archived privileges |
| `list_customers` | Inspect profiles and balances |
| `update_customer` | Update display name and active status |
| `adjust_points` | Award/remove points with reason and retry reference |
| `list_point_ledger` | Inspect a customer's audit ledger |
| `list_redemptions` | Inspect all redemptions or one customer's |

Collections accept `page` (default 1), returning at most 100 rows. An empty page ends the collection. MCP results include `page` and `page_size`; public collections return JSON arrays.

Master records use `{kind,id,name,active,data}`. `data` is a JSON object, allowing tiers, branches, categories, and program settings without SQL schema changes. These records are descriptive; they do not automatically implement earning rules or tier eligibility. Privileges use `{id,name,description,cost,stock,active}`. `stock` is remaining units, and cost must be 1–1,000,000,000 integer points. Archive with `active:false`; historical ledger and redemption snapshots remain intact. Customer balances are maintained exclusively through the ledger, never by profile edits. The service intentionally exposes no raw SQL administration tool.

Example tool arguments:

```json
{"kind":"tier","id":"gold","name":"Gold","active":true,"data":{"minimum_lifetime_points":1000}}
```

```json
{"id":"coffee","name":"Free coffee","description":"One standard coffee","cost":100,"stock":50,"active":true}
```

```json
{"customer_id":"<id>","delta":250,"reason":"Purchase order 123","reference":"order-123"}
```

Reuse the reference only with the same delta and reason. Each adjustment is limited to ±1,000,000,000 points. References beginning `redeem:` are reserved for the redemption ledger.

## Public API

| Method | Endpoint | Authorization |
| --- | --- | --- |
| GET | `/health` | None |
| POST | `/api/v1/register` | None |
| GET | `/api/v1/privileges?page=1` | None |
| GET | `/api/v1/me` | Customer bearer token |
| GET | `/api/v1/me/points` | Customer bearer token |
| GET | `/api/v1/me/ledger?page=1` | Customer bearer token |
| GET | `/api/v1/me/redemptions?page=1` | Customer bearer token |
| POST | `/api/v1/me/redemptions` | Customer bearer token + `Idempotency-Key` |

Register:

```sh
curl -X POST http://localhost:3100/api/v1/register \
  -H 'Content-Type: application/json' \
  -d '{"name":"Alice","email":"alice@example.com"}'
```

Save `access_token` securely from the response. It is returned only during registration; only its SHA-256 hash is stored. Registration creates an account with zero points and normalizes email addresses to lowercase. Duplicate emails return 409 and never disclose an existing token. Email is self-declared, not verified. Customers cannot supply their own points or another customer ID.

View points and redeem:

```sh
curl http://localhost:3100/api/v1/me/points \
  -H "Authorization: Bearer $CUSTOMER_TOKEN"

curl -X POST http://localhost:3100/api/v1/me/redemptions \
  -H "Authorization: Bearer $CUSTOMER_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: checkout-unique-123' \
  -d '{"privilege_id":"coffee"}'
```

Identical redemption retries return the original record (HTTP 200), even if stock is now exhausted. Reusing a key for a different privilege returns 409. Balance debit, stock decrement, redemption snapshot, and ledger entry commit together in one immediate SQLite transaction. Concurrent requests cannot overspend or claim the final stock twice. There is no public endpoint for awarding points; only trusted MCP administrators may do so.

Errors return `{"error":"..."}`: 400 invalid input, 401 unauthorized, 404 missing/unavailable privilege, 409 conflict/insufficient points, 500 internal error.

## Deployment and scope

This is a single-business starter implementation. It provides token-based registration; LINE Login/LIFF, verified email, account recovery, expiring customer sessions, automated earning rules, point expiry, and privilege fulfillment are not integrated. Redemption records are receipts, not merchant-validated single-use vouchers. Deactivating a customer immediately blocks their token; changing `CRM_ADMIN_TOKEN` and restarting rotates administrator access.

For internet deployment, terminate TLS at a reverse proxy, apply registration and API rate limits, restrict allowed origins, and protect admin MCP separately. Do not embed the admin secret in customer applications. All admin-token holders have full CRM access. Customer tokens are long-lived credentials: store them securely. No cross-origin CORS access is enabled by default; serve a customer frontend through the same origin or configure a trusted proxy. Generic master data is not returned through public endpoints. Treat SQLite files as sensitive personal data.

## Development

```sh
go test -race ./...
go vet ./...
```

Tests cover token isolation, schema validation before writes, ledger idempotency, rollback, persistence, concurrent redemption across independent SQLite connections, and the complete MCP-to-public-API workflow: create master data and privileges, register a customer, award points through MCP, redeem through the public API, inspect the ledger, and deactivate the customer. MCP tools also expose the embedded UI resource.

## Docker

```sh
docker build -t centerlinecrm .
docker run --rm -p 127.0.0.1:3100:3100 \
  -e CRM_ADMIN_TOKEN -v centerlinecrm-data:/data centerlinecrm
```

### Build and publish to Docker Hub

`docker-build.sh` follows CenterCDP's publishing workflow. It requires Docker with Buildx and a Docker Hub token with push access. It builds and pushes the image; `task docker-build` remains the local build command.

```sh
read -r -s -p 'Docker Hub token: ' CRM_DOCKER_TOKEN; printf '\n'
printf '%s' "$CRM_DOCKER_TOKEN" | ./docker-build.sh your-user/centerlinecrm 0.1.0 your-user
unset CRM_DOCKER_TOKEN
```

Run the example in Bash. The repository must belong to the supplied Docker Hub username. The script reads the token from stdin, uses a temporary credential helper, and removes its Docker configuration on exit. It does not change your existing Docker login. Run it from any directory; the build context is the project directory.

The default target is `linux/amd64`. Set `PLATFORMS=linux/amd64,linux/arm64` on the script invocation for a multi-platform build; your Buildx builder must support the selected platforms.
