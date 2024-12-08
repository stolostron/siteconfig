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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewConfigurationStore", func() {
	It("creates a new ConfigurationStore object with the given Configuration object", func() {
		testConfigs := []*Configuration{
			{
				AllowReinstalls:         false,
				MaxConcurrentReconciles: 5,
			},
			{
				AllowReinstalls:         true,
				MaxConcurrentReconciles: 1,
			},
			NewDefaultConfiguration(),
		}
		for _, config := range testConfigs {
			configStore := NewConfigurationStore(config)
			Expect(configStore.configuration).To(Equal(config))
		}
	})

	It("returns a nil type when the given Configuration object is nil", func() {
		configStore := NewConfigurationStore(nil)
		Expect(configStore).To(BeNil())
	})
})

var _ = Describe("ConfigurationStore.GetConfiguration", func() {
	It("successfully returns the Configuration object from the ConfigurationStore", func() {
		config := &Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 1,
		}
		configStore := NewConfigurationStore(config)

		Expect(configStore).ToNot(BeNil())
		Expect(configStore.GetConfiguration()).To(Equal(&Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 1,
		}))

		config.AllowReinstalls = false
		Expect(configStore.GetConfiguration()).To(Equal(&Configuration{
			AllowReinstalls:         false,
			MaxConcurrentReconciles: 1,
		}))

		config.MaxConcurrentReconciles = 10
		Expect(configStore.GetConfiguration()).To(Equal(&Configuration{
			AllowReinstalls:         false,
			MaxConcurrentReconciles: 10,
		}))

		config.AllowReinstalls = true
		config.MaxConcurrentReconciles = 5
		Expect(configStore.GetConfiguration()).To(Equal(&Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 5,
		}))
	})

	It("successfully updates the Configuration object from the ConfigurationStore with the given Configuration object", func() {
		config := &Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 1,
		}
		configStore := NewConfigurationStore(config)

		Expect(configStore).ToNot(BeNil())
		Expect(configStore.configuration).To(Equal(&Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 1,
		}))

		config1 := &Configuration{
			AllowReinstalls:         false,
			MaxConcurrentReconciles: 1,
		}
		configStore.SetConfiguration(config1)
		Expect(configStore.configuration).To(Equal(config1))

		config2 := &Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 10,
		}
		configStore.SetConfiguration(config2)
		Expect(configStore.configuration).To(Equal(config2))

	})
})
