.PHONY: fmt test test-integration vet run compose-up compose-down

fmt:
	gofmt -w .

test:
	go test ./...

test-integration:
	TEST_DATABASE_URL='postgres://todo:todo@localhost:5432/todo?sslmode=disable' go test ./internal/store -run TestSyncRoundTrip -count=1

vet:
	go vet ./...

run:
	go run ./cmd/api

compose-up:
	docker compose up --build

compose-down:
	docker compose down
