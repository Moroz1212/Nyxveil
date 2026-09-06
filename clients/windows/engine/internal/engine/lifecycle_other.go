//go:build !windows

package engine

// LifecycleMonitor is a no-op off Windows.
type LifecycleMonitor struct{}

func NewLifecycleMonitor(mgr *Manager) *LifecycleMonitor { return &LifecycleMonitor{} }
func (m *LifecycleMonitor) Start()                       {}
func (m *LifecycleMonitor) Stop()                        {}
func (m *LifecycleMonitor) OnPowerResume()               {}
