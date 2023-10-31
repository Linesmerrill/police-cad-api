// Code generated by mockery v2.10.0. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "github.com/linesmerrill/police-cad-api/models"

	options "go.mongodb.org/mongo-driver/mongo/options"
)

// EmsDatabase is an autogenerated mock type for the EmsDatabase type
type EmsDatabase struct {
	mock.Mock
}

// Find provides a mock function with given fields: _a0, _a1, _a2
func (_m *EmsDatabase) Find(_a0 context.Context, _a1 interface{}, _a2 ...*options.FindOptions) ([]models.Ems, error) {
	_va := make([]interface{}, len(_a2))
	for _i := range _a2 {
		_va[_i] = _a2[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _a0, _a1)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 []models.Ems
	if rf, ok := ret.Get(0).(func(context.Context, interface{}, ...*options.FindOptions) []models.Ems); ok {
		r0 = rf(_a0, _a1, _a2...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]models.Ems)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, interface{}, ...*options.FindOptions) error); ok {
		r1 = rf(_a0, _a1, _a2...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// FindOne provides a mock function with given fields: _a0, _a1, _a2
func (_m *EmsDatabase) FindOne(_a0 context.Context, _a1 interface{}, _a2 ...*options.FindOneOptions) (*models.Ems, error) {
	_va := make([]interface{}, len(_a2))
	for _i := range _a2 {
		_va[_i] = _a2[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _a0, _a1)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 *models.Ems
	if rf, ok := ret.Get(0).(func(context.Context, interface{}, ...*options.FindOneOptions) *models.Ems); ok {
		r0 = rf(_a0, _a1, _a2...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*models.Ems)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, interface{}, ...*options.FindOneOptions) error); ok {
		r1 = rf(_a0, _a1, _a2...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
