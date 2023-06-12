package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockHealthChecker struct {
	mock.Mock
}

func (m *mockHealthChecker) health(dst string) bool {
	args := m.Called(dst)
	return args.Bool(0)
}

func TestBalancer(t *testing.T) {
	serversPool := []serverType{
		{
			dst:       "server1:8080",
			isWorking: &atomic.Bool{},
		}, {
			dst:       "server2:8080",
			isWorking: &atomic.Bool{},
		}, {
			dst:       "server3:8080",
			isWorking: &atomic.Bool{},
		},
	}
	for _, server := range serversPool {
		server.isWorking.Swap(true)
	}
	hc := new(mockHealthChecker)
	b := &Balancer{hc: hc, pool: serversPool}

	t.Run("test getIndex", func(t *testing.T) {
		serversPool[0].dataTransferred = 100
		index, err := b.getIndex()
		assert.Nil(t, err, "error occured when tried to get index")
		assert.Equal(t, 1, index, "getIndex call: expected %d, got %d", 1, index)

		serversPool[1].dataTransferred = 100
		index, err = b.getIndex()
		assert.Nil(t, err, "error occured when tried to get index")
		assert.Equal(t, 2, index, "getIndex call: expected %d, got %d", 2, index)

		serversPool[2].dataTransferred = 150
		index, err = b.getIndex()
		assert.Nil(t, err, "error occured when tried to get index")
		assert.Equal(t, 0, index, "getIndex call: expected %d, got %d", 0, index)

		for i := range b.pool {
			b.pool[i].isWorking.Swap(false)
		}
		index, err = b.getIndex()
		assert.NotNil(t, err, "error didn't occured when all servers stoped, index is %d", index)
	})

	t.Run("test runChecker routine", func(t *testing.T) {
		for i := range b.pool {
			b.pool[i].isWorking.Swap(true)
			b.pool[i].dataTransferred = 0
		}
		b.pool[1].dataTransferred = 1000
		hc.On("health", serversPool[0].dst).Return(false)
		hc.On("health", serversPool[1].dst).Return(true)
		hc.On("health", serversPool[2].dst).Return(false)
		b.runChecker()
		time.Sleep(time.Millisecond)
		index, err := b.getIndex()
		assert.Nil(t, err, "error occured when tried to get index")
		assert.Equal(t, 1, index, "getIndex call: expected %d, got %d", 1, index)
		hc.AssertExpectations(t)
	})
}
