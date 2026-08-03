package provider

import "sync"

// modelSetting holds a provider's current model.
//
// It is guarded because the model is changed at runtime: the /model picker
// and the !model command write it from a Discord event goroutine while an
// in-flight request reads it on another. Providers embed this instead of a
// bare string field so every one of them gets the same protection.
type modelSetting struct {
	mu    sync.RWMutex
	model string
}

func newModelSetting(model string) modelSetting {
	return modelSetting{model: model}
}

func (m *modelSetting) Model() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.model
}

func (m *modelSetting) SetModel(model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.model = model
}
