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
