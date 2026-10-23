package targets

import (
	"fmt"
	"strings"
)

func appSection(name string, targets []Target) bool {
	if !validName(name) {
		return false
	}
	if strings.ContainsAny(name, ".@:") || name == "android" {
		return true
	}
	for _, t := range targets {
		if name == t.PackageName {
			return true
		}
	}
	return false
}
func (m *manager) stopped() (bool, error) {
	for _, p := range []string{m.module + "/disable", m.module + "/remove", "/data/adb/keybox-lite/stopping"} {
		s, _, err := m.disk.lstatEntry(p)
		if err != nil {
			return false, err
		}
		if s.Exists {
			return true, nil
		}
	}
	return false, nil
}
func (m *manager) requireRunning() error {
	stopped, err := m.stopped()
	if err != nil {
		return err
	}
	if stopped {
		return fmt.Errorf("keybox-lite is disabled, removing, or stopping")
	}
	return nil
}
func (m *manager) boot() error {
	return m.locked(func() error {
		stop, err := m.stopped()
		if err != nil || stop {
			return err
		}
		entryErr := m.entryLocked(nil)
		autoErr := m.autoLocked()
		if entryErr != nil && autoErr != nil {
			return fmt.Errorf("entry: %v; auto: %w", entryErr, autoErr)
		}
		if entryErr != nil {
			return entryErr
		}
		return autoErr
	})
}
