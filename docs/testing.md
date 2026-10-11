# Testing Relay

## Prerequisites

- Go installed and available on `PATH`.
- A disposable MongoDB instance for integration tests.
- Network access to the selected MongoDB instance.

## Unit and Package Tests

Run the complete test suite:

```powershell
go test ./...
```

Run static analysis and build verification:

```powershell
go vet ./...
go build ./...
```

## MongoDB Integration Tests

The inspection integration tests use the `MONGODB_TEST_URI` environment variable. Integration tests are skipped when this variable is unset.

### Start Local MongoDB

Ensure Docker Desktop is running, then start a disposable MongoDB container:

```powershell
docker run --name relay-test-mongo -p 27018:27017 -d mongo:8
```

Configure the test connection:

```powershell
$env:MONGODB_TEST_URI = "mongodb://127.0.0.1:27018"
```

Run the inspection integration tests:

```powershell
go test ./internal/handlers -run Integration -v
```

The tests create a unique database for each test and remove it during cleanup. Use only a disposable test database or test cluster with permission to create and drop databases.

### Clean Up

When finished, remove the test container and clear the environment variable:

```powershell
docker rm -f relay-test-mongo
Remove-Item Env:MONGODB_TEST_URI
```

Do not commit database credentials or connection strings containing secrets.

## Event Listing API

The event listing endpoint requires a valid Relay API key.

```http
GET /v1/events
Authorization: Bearer YOUR_RELAY_API_KEY
```

### Query Parameters

| Parameter | Description |
|---|---|
| `delivery_status` | Optional filter: `pending`, `processing`, `delivered`, `failed`, or `disabled` |
| `limit` | Optional page size from 1 to 100. Defaults to 50. |
| `cursor` | Optional opaque cursor returned by the previous page. |

Example request:

```http
GET /v1/events?delivery_status=failed&limit=20
Authorization: Bearer YOUR_RELAY_API_KEY
```

Successful responses contain `events`, `limit`, and `has_more`. When another page exists, the response also includes `next_cursor`. Pass that cursor unchanged to retrieve the next page.

Invalid filters, limits, or cursors return HTTP 400. Missing or invalid API keys return HTTP 401. Database failures return HTTP 503 without exposing internal database details.

### Event Listing Tests

Run the event-listing integration tests against the configured disposable MongoDB instance:

```powershell
go test ./internal/handlers -run EventListIntegration -v
```

These tests verify delivery-status filtering and cursor pagination against MongoDB.
