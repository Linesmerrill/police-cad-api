module github.com/linesmerrill/police-cad-api

go 1.16

require (
	github.com/form3tech-oss/jwt-go v3.2.2+incompatible
	github.com/gorilla/mux v1.8.0
	github.com/stretchr/testify v1.6.1
	go.mongodb.org/mongo-driver v1.5.1
	go.uber.org/zap v1.16.0
)

//heroku specific values:
// +heroku goVersion 1.16
