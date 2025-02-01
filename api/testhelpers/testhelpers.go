package testhelpers

import (
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
)

// CollectionHelperWrapper is a wrapper around the CollectionHelper mock
type CollectionHelperWrapper struct {
	*mocks.CollectionHelper
}

// ToMongoCollection creates a new instance of CollectionHelperWrapper
func (w *CollectionHelperWrapper) ToMongoCollection() *databases.MongoCollection {
	return &databases.MongoCollection{
		// Adjust the fields here to match the actual fields of databases.MongoCollection
		// For example:
		// Field1: w.CollectionHelper.Field1,
		// Field2: w.CollectionHelper.Field2,
	}
}
