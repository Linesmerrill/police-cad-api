module github.com/linesmerrill/police-cad-api

go 1.16

require (
	github.com/form3tech-oss/jwt-go v3.2.5+incompatible
	github.com/gorilla/mux v1.8.0
	github.com/stretchr/testify v1.7.0
	go.mongodb.org/mongo-driver v1.8.1
	go.uber.org/zap v1.19.1
)

//heroku specific values:
// +heroku goVersion 1.16
