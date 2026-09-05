package internal

import (
	"log/slog"
	"sync"

	"github.com/df-mc/go-nethernet"
)

// NewNotifier returns a Notifier that is ready for use. The given logger is
// used to log a debug message when a signal is dropped because a registered
// channel is full.
func NewNotifier(log *slog.Logger) *Notifier {
	return &Notifier{
		notifiers: make(map[nethernet.Notifier]struct{}),
		log:       log,
	}
}

// Notifier distributes incoming [nethernet.Signal] values to a set of
// channels registered with [Notifier.Register].
type Notifier struct {
	notifiers map[nethernet.Notifier]struct{}
	mu        sync.RWMutex
	log       *slog.Logger
}

// Register adds signals to the set of channels notified by [Notifier.Notify]
// and returns a stop function that removes and closes the channel. The caller
// must not close the channel themselves.
func (n *Notifier) Register(notifier nethernet.Notifier) (stop func()) {
	n.mu.Lock()
	n.notifiers[notifier] = struct{}{}
	n.mu.Unlock()

	return func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.stop(notifier)
	}
}

// Signal sends signal to all registered channels. If a channel is not ready
// to receive, the signal is dropped for that channel and a debug message is
// logged.
func (n *Notifier) Signal(signal *nethernet.Signal) {
	n.mu.RLock()
	for notifier := range n.notifiers {
		notifier.NotifySignal(signal)
	}
	n.mu.RUnlock()
}

// stop removes the channel registered with the given ID and closes it.
// The caller must hold mu before calling stop.
func (n *Notifier) stop(notifier nethernet.Notifier) {
	delete(n.notifiers, notifier)
}

// Close unregisters and closes all registered channels.
func (n *Notifier) Close() error {
	n.mu.Lock()
	for notifier := range n.notifiers {
		n.stop(notifier)
	}
	n.mu.Unlock()

	return nil
}
