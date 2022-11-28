// Code generated by MockGen. DO NOT EDIT.
// Source: pkg/k8s/exec.go

// Package mock_k8s is a generated GoMock package.
package mock_k8s

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
)

// MockSyncExecutorInterface is a mock of SyncExecutorInterface interface.
type MockSyncExecutorInterface struct {
	ctrl     *gomock.Controller
	recorder *MockSyncExecutorInterfaceMockRecorder
}

// MockSyncExecutorInterfaceMockRecorder is the mock recorder for MockSyncExecutorInterface.
type MockSyncExecutorInterfaceMockRecorder struct {
	mock *MockSyncExecutorInterface
}

// NewMockSyncExecutorInterface creates a new mock instance.
func NewMockSyncExecutorInterface(ctrl *gomock.Controller) *MockSyncExecutorInterface {
	mock := &MockSyncExecutorInterface{ctrl: ctrl}
	mock.recorder = &MockSyncExecutorInterfaceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockSyncExecutorInterface) EXPECT() *MockSyncExecutorInterfaceMockRecorder {
	return m.recorder
}

// ExecContainer mocks base method.
func (m *MockSyncExecutorInterface) ExecContainer(ctx context.Context, namespace, pod, container string, command ...string) (int, string, string, error) {
	m.ctrl.T.Helper()
	varargs := []interface{}{ctx, namespace, pod, container}
	for _, a := range command {
		varargs = append(varargs, a)
	}
	ret := m.ctrl.Call(m, "ExecContainer", varargs...)
	ret0, _ := ret[0].(int)
	ret1, _ := ret[1].(string)
	ret2, _ := ret[2].(string)
	ret3, _ := ret[3].(error)
	return ret0, ret1, ret2, ret3
}

// ExecContainer indicates an expected call of ExecContainer.
func (mr *MockSyncExecutorInterfaceMockRecorder) ExecContainer(ctx, namespace, pod, container interface{}, command ...interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	varargs := append([]interface{}{ctx, namespace, pod, container}, command...)
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ExecContainer", reflect.TypeOf((*MockSyncExecutorInterface)(nil).ExecContainer), varargs...)
}
