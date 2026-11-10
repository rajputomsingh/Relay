## Source
Commit: 69271e60f92eca4464d200da945c23113418ba6c
Branch: main
Tree status: 10 modified files

## Runtime
Go: go version go1.27.0 windows/amd64
Docker: 29.4.2
Docker Compose: Docker Compose version v5.1.3
MongoDB image: mongo:7.0

## Test invocation
Command: $env:MONGODB_TEST_URI='mongodb://localhost:27017'; Remove-Item Env:MONGODB_TEST_CONTAINER; go test ./... -count=1 -timeout 300s -v
Skipped: TestHandlerRecoversFromMongoOutage (requires MONGODB_TEST_CONTAINER)

## MongoDB index inventory
Verified via: docker exec relay-mongo mongosh relay --quiet --eval '...'

