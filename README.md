# CenterLINECRM

A SQLite loyalty CRM built from CenterCDP's Go, Streamable HTTP MCP, and embedded MCP Apps patterns. An LLM can manage master data through typed MCP tools, while customers register, view points, browse privileges, and redeem them through a separate public JSON API.

## Run

Requires Go 1.26+, CGO and a C compiler (SQLite uses `mattn/go-sqlite3`).

```sh
go build -o build/centerlinecrm ./cmd/server
./build/centerlinecrm --database ./data/centerlinecrm.db --address 127.0.0.1:8000
```

The database directory and version-1 schema are created automatically. The server defaults to localhost, port 8000. Keep the database, `-wal` and `-shm` files together; use SQLite's online backup API or stop the server before copying the database. Existing databases are preserved on startup.

## Connect an MCP client

Use a client that supports Streamable HTTP. The MCP endpoint does not require an authentication header. Configuration shape varies by client:

```json
{
  "mcpServers": {
    "centerlinecrm": {
      "url": "http://localhost:8000/mcp"
    }
  }
}
```

Ask the LLM to open the CRM admin UI, create a Gold tier, add a coffee privilege costing 100 points, or award a customer's purchase points. The LLM runs in your MCP client; this server does not need an LLM API key.

`open_crm_admin` accepts an optional `section` (`master`, `privileges`, `customers`, `points`, or `redemptions`) and `customer_id`. For example, `{"section":"points","customer_id":"<id>"}` opens that customer’s point ledger. LLM tool results automatically select the matching tab, preserve filters, and populate editor fields. Tool arguments are validated on the server, including required fields and unknown properties.

`open_crm_admin` exposes an embedded MCP App at `ui://centerlinecrm/admin/v1.html`. Compatible clients render tabs, editable forms, tables, and pagination. Every edit calls the same MCP tools. Clients without MCP Apps can use all tools directly.

| Tool | Purpose |
| --- | --- |
| `open_crm_admin` | Open the admin editor |
| `update_business_branding` | Set business name, logo, and primary color on the public member page |
| `get_public_urls` | Get the public membership registration URL |
| `get_member_coupon_url` | Return a personalized link directly to a member’s coupon page |
| `save_master_record` | Create/update/archive arbitrary master records |
| `list_master_records` | List master records, optionally by kind |
| `save_privilege` | Create/update/archive a redeemable privilege |
| `list_privileges` | List active and archived privileges |
| `list_customers` | Inspect profiles and balances |
| `bulk_import_members` | Import/update up to 500 members from CDP or another source |
| `bulk_adjust_points` | Apply up to 500 audited point adjustments with safe retries |
| `update_customer` | Update display name and active status |
| `adjust_points` | Award/remove points with reason and retry reference |
| `list_point_ledger` | Inspect a customer's audit ledger |
| `list_redemptions` | Inspect all redemptions or one customer's |

List collections accept `page` (default 1), returning at most 100 rows. An empty page ends the collection. MCP results include `page` and `page_size`; public collections return JSON arrays.

Master records use `{kind,id,name,active,data}`. Omit `id` when creating master records or privileges/coupons: the server generates the ID and returns it as `data.saved`, along with `data.record`. Supply that returned ID only to edit an existing record; an unknown supplied ID returns an error. Existing IDs continue to work for edits. The admin editor hides the ID on new records, shows it read-only for edits, and retains the generated ID after saving. `data` is a JSON object, allowing tiers, branches, categories, and program settings without SQL schema changes. These records are descriptive; they do not automatically implement earning rules or tier eligibility. Privileges use `{id,name,description,cost,stock,active}`. `stock` is remaining units, and cost must be 1–1,000,000,000 integer points. Archive with `active:false`; historical ledger and redemption snapshots remain intact. Customer balances are maintained exclusively through the ledger, never by profile edits. The service intentionally exposes no raw SQL administration tool.

Example tool arguments:

```json
{"kind":"tier","name":"Gold","active":true,"data":{"minimum_lifetime_points":1000}}
```

```json
{"name":"Free coffee","description":"One standard coffee","cost":100,"stock":50,"active":true}
```

```json
{"customer_id":"<id>","delta":250,"reason":"Purchase order 123","reference":"order-123"}
```

Reuse the reference only with the same delta and reason. Each adjustment is limited to ±1,000,000,000 points. References beginning `redeem:` are reserved for the redemption ledger.

## Import contacts and points from CDP or another source

Call `bulk_import_members` with a stable source namespace and 1–500 flat member rows. For CenterCDP, map the contact's name (such as `attributes.guest_name`) to `name`, its email identity to `email`, and its `contact_id` to `external_id`. Include the CDP database ID in `source` to separate businesses.

```json
{
  "source":"centercdp:serenity-pearl-db-id",
  "members":[
    {"external_id":"cdp-contact-emily","name":"Emily Wilson","email":"emily.wilson@example.com"},
    {"external_id":"cdp-contact-nattapong","name":"Nattapong Srisawat","email":"nattapong.srisawat@example.com"}
  ]
}
```

CRM member IDs are generated automatically. The import matches existing members by normalized email or the stored `(source, external_id)` mapping. It updates their names and email addresses while preserving point balances and existing member access. `external_id` is optional; omit it to match only by email. `active` is optional: new members default to active, and existing members retain their status when it is omitted. Conflicting source identities or multiple rows resolving to the same member reject the batch. Repeating an import updates the same members rather than creating duplicates.

The result reports `created`, `updated`, and `members`, including each CRM customer ID and source ID mapping. Newly created members start with zero points. Call `get_member_coupon_url` with the returned CRM ID, email, or source/external ID to return a personal coupon link to the caller. Import responses do not expose raw member keys. No messages are sent automatically.

Call `bulk_adjust_points` to award or remove points. Select exactly one `customer_id`, `email`, or `external_id` per row; external IDs require the same top-level `source` used for import. Email or external IDs allow bulk updates without entering CRM IDs manually.

```json
{
  "source":"centercdp:serenity-pearl-db-id",
  "adjustments":[
    {"external_id":"cdp-contact-emily","delta":300,"reason":"CDP opening balance","reference":"opening-2026-11"},
    {"email":"nattapong.srisawat@example.com","delta":200,"reason":"CDP opening balance","reference":"opening-2026-11"}
  ]
}
```

`delta` adds to/subtracts from the current balance; it does not replace it. Each row needs a reason and a stable external transaction `reference` unique per member. Identical retries return the original ledger entry without applying points again. Changing the delta or reason for an existing reference returns a conflict. Duplicate member/reference pairs within a batch are rejected. Use separate references for distinct transactions. Adjustments are limited to nonzero ±1,000,000,000 points and cannot create negative balances or award new transactions to inactive members. Import balances only once with a stable opening-balance reference.

Each bulk call is atomic: an invalid or conflicting row rolls back all changes in that call. Errors identify the failing row (one-based). Member import and point adjustment are separate transactions; if points fail after a successful import, fix and retry the point batch.

## Public membership registration

Open `http://localhost:8000/public/register` to register a customer by name and email. `/public` and `/public/` redirect to this page. The responsive public page is embedded in the Go binary and served by the same service as `/mcp`; no frontend build or separate hosting is required.

Use `update_business_branding` to customize the business name, logo, and primary color on both the registration and member views:

```json
{"business_name":"My Coffee Club","logo_url":"https://example.com/logo.png","primary_color":"#2563eb"}
```

All three fields are required; each call replaces the branding. The name must contain 1–200 bytes after trimming, the logo must be an absolute HTTPS URL without credentials or a fragment (use `""` to remove it), and the color must use six-digit `#RRGGBB` notation. Settings persist in SQLite and appear on the next page load. The color updates buttons, the brand mark, and the membership points card, with a contrasting text color. Without saved settings, the page uses CenterLINECRM branding. The logo loads from its external host; the page sends no referrer.

When asked for a public URL, MCP clients can call `get_public_urls` with `{}`. Set `CRM_BASE_URL` to the externally accessible service base, including any instance prefix, for example `https://centermcp.com/instance-123` or `https://members.example.com`. The tool appends `/public/register`; trailing slashes are removed. Use an HTTP or HTTPS URL without credentials, query strings, or fragments, and omit `/mcp` and `/public/register` from the base. With `CRM_BASE_URL` unset or blank, the result is `https://centermcp.com/<mcp-id>/public/register` with `is_template: true`; replace `<mcp-id>` before sharing. With a configured base, `is_template` is false. Invalid configuration returns a tool error.

For a dedicated MCP domain, share `https://your-mcp-domain/public/register`. If the MCP endpoint is `https://your-domain/<instance>/mcp`, share `https://your-domain/<instance>/public/register`. Relative asset and API URLs preserve the instance prefix when the proxy strips it. Forward `/public/*` and `/api/v1/*` to the same CRM service alongside `/mcp`.

Registration immediately opens the member's points card and coupon collection at `/public/coupons`. A persistent HttpOnly browser session keeps the member signed in for 30 days, including after refresh. Consumers do not see access keys, download membership files, or manage credentials. Duplicate registrations suggest opening the personal member link or contacting the membership team for one.

After signup, the mobile loyalty interface shows active privileges as coupons with descriptions, point costs, stock, and redemption buttons. Insufficient points or exhausted stock disable redemption. Customers can refresh points and coupons and load further pages. Redemption uses the browser session and an idempotency key, displays the receipt, and refreshes balance and availability. Unconfirmed attempts retain the same idempotency key while the page remains open.

### Personalized coupon links

Call **`get_member_coupon_url`** to return a URL directly to a member's coupon page:

```json
{"email":"emily.wilson@example.com"}
```

For an imported CDP contact:

```json
{"source":"centercdp:serenity-pearl-db-id","external_id":"cdp-contact-emily"}
```

Select exactly one `customer_id`, `email`, or `external_id`; external IDs require their import `source`. `CRM_BASE_URL` must be configured with the public service base, including any instance prefix. The response includes `customer_id`, `coupon_url`, and `expires_at`. Example URL shape: `https://centermcp.com/instance-123/public/coupons#member=<opaque-value>`.

The link opens coupons without registration, and lasts 90 days. It is specific to that member and grants access to their membership, so return it to the intended member. Each call creates an independent link without invalidating earlier links or sessions. Expired links can be replaced by calling the tool again. Inactive members cannot receive or open links, and existing browser sessions stop working when a member is deactivated.

The browser exchanges the URL fragment through `POST /api/v1/member-session`, establishes an HttpOnly, SameSite=Strict session cookie, then removes the fragment from the address bar. Link values are not sent in the initial page request or referrer. Only hashes are stored in SQLite. The cookie path defaults to the external API path, preserving MCP instance prefixes when the proxy strips them. Cookies use `Secure` for direct TLS or `X-Forwarded-Proto: https`; configure that header on a trusted HTTPS reverse proxy. Opening `/public/coupons` again on the same browser resumes the session. A new browser can use the original personal member link.

## Public API

| Method | Endpoint | Authorization |
| --- | --- | --- |
| GET | `/health` | None |
| POST | `/api/v1/register` | None |
| GET | `/api/v1/privileges?page=1` | None |
| POST | `/api/v1/member-session` | Personal member link value |
| GET | `/api/v1/me` | Member session or API bearer token |
| GET | `/api/v1/me/points` | Member session or API bearer token |
| GET | `/api/v1/me/ledger?page=1` | Member session or API bearer token |
| GET | `/api/v1/me/redemptions?page=1` | Member session or API bearer token |
| POST | `/api/v1/me/redemptions` | Member session or API bearer token + `Idempotency-Key` |

Register:

```sh
curl -c /tmp/crm-cookies.txt -X POST http://localhost:8000/api/v1/register \
  -H 'Content-Type: application/json' \
  -d '{"name":"Alice","email":"alice@example.com"}'
```

Registration sets the browser session cookie. Existing API integrations can continue using the returned `access_token` as a bearer token; the consumer page ignores it. Registration creates an account with zero points and normalizes email addresses to lowercase. Duplicate emails return 409. Email is self-declared, not verified. Customers cannot supply their own points or another customer ID.

View points and redeem:

```sh
curl http://localhost:8000/api/v1/me/points \
  -b /tmp/crm-cookies.txt

curl -X POST http://localhost:8000/api/v1/me/redemptions \
  -b /tmp/crm-cookies.txt \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: checkout-unique-123' \
  -d '{"privilege_id":"<generated-privilege-id>"}'
```

Identical redemption retries return the original record (HTTP 200), even if stock is now exhausted. Reusing a key for a different privilege returns 409. Balance debit, stock decrement, redemption snapshot, and ledger entry commit together in one immediate SQLite transaction. Concurrent requests cannot overspend or claim the final stock twice. There is no public endpoint for awarding points; point adjustments are exposed through MCP.

Errors return `{"error":"..."}`: 400 invalid input, 401 unauthorized, 404 missing/unavailable privilege, 409 conflict/insufficient points, 500 internal error.

## Deployment and scope

This is a single-business starter implementation. It provides registration, personalized member links, and expiring browser sessions. LINE Login/LIFF, verified email, self-service email recovery, automated earning rules, point expiry, and privilege fulfillment are not integrated. Redemption records are receipts, not merchant-validated single-use vouchers. Deactivating a customer immediately blocks their links, browser sessions, and API token.

For internet deployment, terminate TLS at a reverse proxy, apply registration and API rate limits, restrict allowed origins, and protect admin MCP separately. The MCP endpoint has no built-in authentication: anyone who can reach `/mcp` can manage CRM data and points. Personalized URLs grant member access; return each URL only to the intended member. No cross-origin CORS access is enabled by default; serve a customer frontend through the same origin or configure a trusted proxy. Generic master data is not returned through public endpoints. Treat SQLite files as sensitive personal data.

## Development

```sh
go test -race ./...
go vet ./...
node --test tests/*.test.cjs
```

Tests cover token isolation, schema validation before writes, ledger idempotency, rollback, persistence, concurrent redemption across independent SQLite connections, and the complete MCP-to-public-API workflow: create master data and privileges, register a customer, award points through MCP, redeem through the public API, inspect the ledger, and deactivate the customer. MCP tests run without Authorization headers and also verify the embedded UI resource.

## Docker

```sh
docker build -t centerlinecrm .
docker run --rm -p 127.0.0.1:8000:8000 \
  -v centerlinecrm-data:/data centerlinecrm
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
