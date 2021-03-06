// Code generated by mockery v2.10.0. DO NOT EDIT.

package mocks

import (
	databases "github.com/linesmerrill/police-cad-api/databases"
	mock "github.com/stretchr/testify/mock"

	mongo "go.mongodb.org/mongo-driver/mongo"
)

// ClientHelper is an autogenerated mock type for the ClientHelper type
type ClientHelper struct {
	mock.Mock
}

// Connect provides a mock function with given fields:
func (_m *ClientHelper) Connect() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Database provides a mock function with given fields: _a0
func (_m *ClientHelper) Database(_a0 string) databases.DatabaseHelper {
	ret := _m.Called(_a0)

	var r0 databases.DatabaseHelper
	if rf, ok := ret.Get(0).(func(string) databases.DatabaseHelper); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(databases.DatabaseHelper)
		}
	}

	return r0
}

// StartSession provides a mock function with given fields:
func (_m *ClientHelper) StartSession() (mongo.Session, error) {
	ret := _m.Called()

	var r0 mongo.Session
	if rf, ok := ret.Get(0).(func() mongo.Session); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(mongo.Session)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
