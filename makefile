test:mocks
	go test ./...

run:swagger
	go run main.go

cover:mocks
	go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out

mocks:
	mockery --dir databases --all --output ./databases/mocks

check-swagger:mocks
	which swagger || (GO111MODULE=off go get -u github.com/go-swagger/go-swagger/cmd/swagger)

swagger: check-swagger
	GO111MODULE=on go mod vendor  && GO111MODULE=off swagger generate spec -o ./docs/swagger.yaml --scan-models

serve-swagger: check-swagger
	swagger serve -F=swagger docs/swagger.yaml

check-procfile:
	@echo "Checking Procfile..."
	@if [ ! -f Procfile ]; then \
		echo "❌ ERROR: Procfile is missing! This will cause Heroku deployment to fail."; \
		echo "Create a Procfile with: web: bin/police-cad-api"; \
		exit 1; \
	fi
	@if ! grep -q "^web:" Procfile; then \
		echo "❌ ERROR: Procfile must contain a 'web' process type!"; \
		cat Procfile; \
		exit 1; \
	fi
	@if ! grep -q "^web: bin/police-cad-api" Procfile; then \
		echo "❌ ERROR: Procfile web process must be: web: bin/police-cad-api"; \
		cat Procfile; \
		exit 1; \
	fi
	@echo "✅ Procfile is valid"
	@cat Procfile

pre-push: check-procfile mocks test
	@echo "✅ All pre-push checks passed"