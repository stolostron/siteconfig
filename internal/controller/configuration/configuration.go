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
)

// Default configuration values for ClusterInstance.
const (
	// DefaultAllowReinstalls specifies whether reinstallation is allowed by default.
	DefaultAllowReinstalls = false

	// DefaultMaxConcurrentReconciles defines the default maximum number of concurrent reconciles.
	DefaultMaxConcurrentReconciles = 1
)

// Configuration defines the runtime settings for the ClusterInstance controller.
// These settings can be customized to control specific behaviors.
type Configuration struct {
	// allowReinstalls specifies whether the controller permits reinstallation operations.
	// This setting determines if reinstallation requests are processed.
	allowReinstalls bool

	// maxConcurrentReconciles specifies the maximum number of ClusterInstance resources
	// that can be reconciled concurrently by the controller.
	// To apply changes to this value after the controller has started, the siteconfig-manager
	// pod must be restarted.
	maxConcurrentReconciles int
}

// NewDefaultConfiguration creates a new Configuration instance with default settings.
func NewDefaultConfiguration() *Configuration {
	return &Configuration{
		allowReinstalls:         DefaultAllowReinstalls,
		maxConcurrentReconciles: DefaultMaxConcurrentReconciles,
	}
}

// ToMap converts the Configuration object into a map of string keys and values.
func (c *Configuration) ToMap() map[string]string {
	return map[string]string{
		"allowReinstalls":         strconv.FormatBool(c.allowReinstalls),
		"maxConcurrentReconciles": strconv.Itoa(c.maxConcurrentReconciles),
	}
}

// FromMap updates the Configuration object based on values from a provided map.
// An error if any input key is unsupported or if value conversion fails.
func (c *Configuration) FromMap(input map[string]string) error {
	for key, value := range input {
		switch key {
		case "allowReinstalls":
			boolValue, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("invalid value for %q: %w", key, err)
			}
			c.allowReinstalls = boolValue

		case "maxConcurrentReconciles":
			intValue, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid value for %q: %w", key, err)
			}
			c.maxConcurrentReconciles = intValue

		default:
			return fmt.Errorf("unsupported key in input map: %q", key)
		}
	}
	return nil
}
