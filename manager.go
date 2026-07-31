package slogs

import (
	"errors"
	"fmt"
	"sync"
)

// Manager manages named logger instances.
type Manager struct {
	loggers map[string]*Logger
	mu      sync.RWMutex
}

var (
	globalManager *Manager
	managerOnce   sync.Once
)

// defaultManager returns the singleton Manager instance.
func defaultManager() *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{loggers: make(map[string]*Logger)}
	})
	return globalManager
}

// CreateLogger creates and registers a named logger.
func CreateLogger(name string, config Config) (*Logger, error) {
	return defaultManager().Create(name, config)
}

// GetLogger retrieves a previously created named logger.
func GetLogger(name string) (*Logger, bool) {
	return defaultManager().Get(name)
}

// RemoveLogger removes and closes a named logger from the registry.
func RemoveLogger(name string) error {
	return defaultManager().Remove(name)
}

// CloseAll closes all managed loggers, closes the default logger,
// and resets the registry.
func CloseAll() error {
	return defaultManager().CloseAll()
}

// Create creates and registers a named logger.
func (m *Manager) Create(name string, config Config) (*Logger, error) {
	if name == "" {
		return nil, fmt.Errorf("logging: logger name cannot be empty")
	}
	m.mu.RLock()
	_, exists := m.loggers[name]
	m.mu.RUnlock()
	if exists {
		return nil, fmt.Errorf("logging: logger already exists: %s", name)
	}

	logger, err := NewLogger(config)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.loggers[name] = logger
	m.mu.Unlock()
	return logger, nil
}

// Get retrieves a named logger.
func (m *Manager) Get(name string) (*Logger, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	logger, ok := m.loggers[name]
	return logger, ok
}

// Remove removes and closes a named logger from the registry.
// Returns an error if the logger does not exist.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger, ok := m.loggers[name]
	if !ok {
		return fmt.Errorf("logging: logger not found: %s", name)
	}

	delete(m.loggers, name)
	return logger.Close()
}

// CloseAll closes all managed loggers, closes the default logger,
// and clears the registry.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for name, logger := range m.loggers {
		if err := logger.Close(); err != nil {
			errs = append(errs, fmt.Errorf("logging: close %q: %w", name, err))
		}
	}
	m.loggers = make(map[string]*Logger)

	// Close and reset the default logger so Init can be called again.
	if err := closeDefault(); err != nil {
		errs = append(errs, fmt.Errorf("logging: close default logger: %w", err))
	}

	return errors.Join(errs...)
}