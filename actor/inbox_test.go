package actor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInboxSendAndProcess(t *testing.T) {
	inbox := NewInbox(10)
	processedMessages := make(chan Envelope, 10)
	mockProc := MockProcesser{
		processFunc: func(envelopes []Envelope) {
			for _, e := range envelopes {
				processedMessages <- e
			}
		},
	}
	inbox.Start(mockProc)
	msg := Envelope{}
	inbox.Send(msg)
	select {
	case <-processedMessages: // Message processed
	case <-time.After(time.Millisecond):
		t.Errorf("Message was not processed in time")
	}

	inbox.Stop()
}

func TestInboxSendAndProcessMany(t *testing.T) {
    iterations := 100000
    for i := 0; i < iterations; i++ {
        t.Run(fmt.Sprintf("iteration-%d", i), func(t *testing.T) {
            t.Parallel()
            inbox := NewInbox(10)
            processedMessages := make(chan Envelope, 10)
		mockProc := MockProcesser{
			processFunc: func(envelopes []Envelope) {
				for _, e := range envelopes {
					processedMessages <- e
				}
			},
		}
		inbox.Start(mockProc)
  timer := time.NewTimer(time.Second)
  defer timer.Stop()
  select {
  case <-processedMessages: // Message processed
  case <-timer.C:
      t.Errorf("Message was not processed in time")
  }
			t.Errorf("Message was not processed in time")
		}
		timer.Stop()

		inbox.Stop()
	}
}

type MockProcesser struct {
	processFunc func([]Envelope)
}

func (m MockProcesser) Start() {}
func (m MockProcesser) PID() *PID {
	return nil
}
func (m MockProcesser) Send(*PID, any, *PID) {}
func (m MockProcesser) Invoke(envelopes []Envelope) {
	m.processFunc(envelopes)
}
func (m MockProcesser) Shutdown(_ *sync.WaitGroup) {}

func TestInboxStop(t *testing.T) {
	inbox := NewInbox(10)
	done := make(chan struct{})
	mockProc := MockProcesser{
		processFunc: func(envelopes []Envelope) {
			inbox.Stop()
			done <- struct{}{}
		},
	}
	inbox.Start(mockProc)
	inbox.Send(Envelope{})
	<-done
	require.True(t, atomic.LoadInt32(&inbox.procStatus) == stopped)
}
