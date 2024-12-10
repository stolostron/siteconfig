/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package configuration

import (
	"fmt"
	"strconv"
	"sync"
)

// ConfigurationStore provides thread-safe access to the runtime configuration for the
// ClusterInstance controller. It allows dynamic updates to configuration fields, such
// as reinstallation permissions and the number of concurrent reconciles.
type ConfigurationStore struct {
	// configuration holds the current runtime configuration settings.
	configuration *Configuration

	// mu is a read-write mutex to ensure synchronized access to the configuration.
	mu sync.RWMutex
}

// NewConfigurationStore initializes a new ConfigurationStore with the given initial configuration.
func NewConfigurationStore(initialConfig *Configuration) (*ConfigurationStore, error) {
	if initialConfig == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	return &ConfigurationStore{
		configuration: initialConfig,
	}, nil
}

// SetAllowReinstalls updates the allowReinstalls field in the configuration.
func (s *ConfigurationStore) SetAllowReinstalls(allowReinstalls bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configuration.allowReinstalls = allowReinstalls
}

// GetAllowReinstalls retrieves the current value of the allowReinstalls field.
func (s *ConfigurationStore) GetAllowReinstalls() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configuration.allowReinstalls
}

// SetMaxConcurrentReconciles updates the maxConcurrentReconciles field in the configuration.
func (s *ConfigurationStore) SetMaxConcurrentReconciles(maxConcurrentReconciles int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configuration.maxConcurrentReconciles = maxConcurrentReconciles
}

// GetMaxConcurrentReconciles retrieves the current value of the maxConcurrentReconciles field.
func (s *ConfigurationStore) GetMaxConcurrentReconciles() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configuration.maxConcurrentReconciles
}

// UpdateConfiguration applies a set of configuration changes provided as key-value pairs.
// Supported keys are:
// - "allowReinstalls": A boolean string value (true/false) indicating whether reinstallation operations are allowed.
// - "maxConcurrentReconciles": An integer string value specifying the maximum number of concurrent reconciles.
//
// Returns an error if any value cannot be parsed or if an unsupported key is provided.
func (s *ConfigurationStore) UpdateConfiguration(data map[string]string) error {
	for key, value := range data {
		switch key {
		case "allowReinstalls":
			allowReinstalls, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("invalid value for %q: %w", key, err)
			}
			if s.GetAllowReinstalls() != allowReinstalls {
				s.SetAllowReinstalls(allowReinstalls)
			}

		case "maxConcurrentReconciles":
			maxConcurrentReconciles, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid value for %q: %w", key, err)
			}
			if s.GetMaxConcurrentReconciles() != maxConcurrentReconciles {
				s.SetMaxConcurrentReconciles(maxConcurrentReconciles)
			}

		default:
			return fmt.Errorf("unsupported key in input map: %q", key)
		}
	}
	return nil
}
