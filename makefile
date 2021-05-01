cover:
	go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out

mocks:
	mockery --dir databases --all