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
				allowReinstalls:         false,
				maxConcurrentReconciles: 5,
			},
			{
				allowReinstalls:         true,
				maxConcurrentReconciles: 1,
			},
			NewDefaultConfiguration(),
		}
		for _, config := range testConfigs {
			configStore, err := NewConfigurationStore(config)
			Expect(err).ToNot(HaveOccurred())
			Expect(configStore.configuration).To(Equal(config))
		}
	})

	It("returns a nil type when the given Configuration object is nil", func() {
		configStore, err := NewConfigurationStore(nil)
		Expect(err).To(HaveOccurred())
		Expect(configStore).To(BeNil())
	})
})

var _ = Describe("ConfigurationStore.UpdateConfiguration", func() {
	It("successfully updates the Configuration object from the ConfigurationStore with the given data map", func() {
		config := &Configuration{
			allowReinstalls:         true,
			maxConcurrentReconciles: 1,
		}
		configStore, err := NewConfigurationStore(config)
		Expect(err).ToNot(HaveOccurred())

		Expect(configStore.GetAllowReinstalls()).To(BeTrue())
		Expect(configStore.GetMaxConcurrentReconciles()).To(Equal(1))

		data := map[string]string{
			"allowReinstalls":         "false",
			"maxConcurrentReconciles": "1",
		}

		err = configStore.UpdateConfiguration(data)
		Expect(err).ToNot(HaveOccurred())
		Expect(configStore.GetAllowReinstalls()).To(BeFalse())
		Expect(configStore.GetMaxConcurrentReconciles()).To(Equal(1))

		data["maxConcurrentReconciles"] = "10"
		err = configStore.UpdateConfiguration(data)
		Expect(err).ToNot(HaveOccurred())
		Expect(configStore.GetAllowReinstalls()).To(BeFalse())
		Expect(configStore.GetMaxConcurrentReconciles()).To(Equal(10))
	})
})
