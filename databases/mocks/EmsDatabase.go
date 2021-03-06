// Code generated by mockery v2.10.0. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "github.com/linesmerrill/police-cad-api/models"
)

// EmsDatabase is an autogenerated mock type for the EmsDatabase type
type EmsDatabase struct {
	mock.Mock
}

// Find provides a mock function with given fields: ctx, filter
func (_m *EmsDatabase) Find(ctx context.Context, filter interface{}) ([]models.Ems, error) {
	ret := _m.Called(ctx, filter)

	var r0 []models.Ems
	if rf, ok := ret.Get(0).(func(context.Context, interface{}) []models.Ems); ok {
		r0 = rf(ctx, filter)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]models.Ems)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, interface{}) error); ok {
		r1 = rf(ctx, filter)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// FindOne provides a mock function with given fields: ctx, filter
func (_m *EmsDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Ems, error) {
	ret := _m.Called(ctx, filter)

	var r0 *models.Ems
	if rf, ok := ret.Get(0).(func(context.Context, interface{}) *models.Ems); ok {
		r0 = rf(ctx, filter)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*models.Ems)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, interface{}) error); ok {
		r1 = rf(ctx, filter)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
