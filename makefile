test:
	go test ./...

run:swagger
	go run main.go

cover:
	go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out

mocks:
	mockery --dir databases --all --output ./databases/mocks
check-swagger:
	which swagger || (GO111MODULE=off go get -u github.com/go-swagger/go-swagger/cmd/swagger)

swagger: check-swagger
	GO111MODULE=on go mod vendor  && GO111MODULE=off swagger generate spec -o ./docs/swagger.yaml --scan-models

serve-swagger: check-swagger
	swagger serve -F=swagger docs/swagger.yaml