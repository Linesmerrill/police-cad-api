// Code generated by mockery v2.10.0. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "github.com/linesmerrill/police-cad-api/models"
)

// FirearmDatabase is an autogenerated mock type for the FirearmDatabase type
type FirearmDatabase struct {
	mock.Mock
}

// Find provides a mock function with given fields: ctx, filter
func (_m *FirearmDatabase) Find(ctx context.Context, filter interface{}) ([]models.Firearm, error) {
	ret := _m.Called(ctx, filter)

	var r0 []models.Firearm
	if rf, ok := ret.Get(0).(func(context.Context, interface{}) []models.Firearm); ok {
		r0 = rf(ctx, filter)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]models.Firearm)
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
func (_m *FirearmDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Firearm, error) {
	ret := _m.Called(ctx, filter)

	var r0 *models.Firearm
	if rf, ok := ret.Get(0).(func(context.Context, interface{}) *models.Firearm); ok {
		r0 = rf(ctx, filter)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*models.Firearm)
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