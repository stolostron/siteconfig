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

var _ = Describe("NewDefaultConfiguration", func() {
	It("creates a new Configuration object with default values for the SiteConfig Operator", func() {
		expected := &Configuration{
			allowReinstalls:         DefaultAllowReinstalls,
			maxConcurrentReconciles: DefaultMaxConcurrentReconciles,
		}
		gotConfig := NewDefaultConfiguration()
		Expect(gotConfig).To(Equal(expected))
	})
})

var _ = Describe("Configuration.ToMap", func() {
	It("correctly converts Configuration objects to maps", func() {

		testConfigs := []Configuration{
			{
				allowReinstalls:         false,
				maxConcurrentReconciles: 5,
			},
			{
				allowReinstalls:         true,
				maxConcurrentReconciles: 1,
			},
		}
		expectedMaps := []map[string]string{
			{
				"allowReinstalls":         "false",
				"maxConcurrentReconciles": "5",
			},
			{
				"allowReinstalls":         "true",
				"maxConcurrentReconciles": "1",
			},
		}

		for index, config := range testConfigs {
			got := config.ToMap()
			Expect(got).To(Equal(expectedMaps[index]))
		}
	})
})

var _ = Describe("FromMap", func() {
	It("correctly converts maps to Configuration objects", func() {
		testMaps := []map[string]string{
			{
				"allowReinstalls":         "false",
				"maxConcurrentReconciles": "5",
			},
			{
				"allowReinstalls":         "true",
				"maxConcurrentReconciles": "1",
			},
		}

		expectedConfigs := []Configuration{
			{
				allowReinstalls:         false,
				maxConcurrentReconciles: 5,
			},
			{
				allowReinstalls:         true,
				maxConcurrentReconciles: 1,
			},
		}

		for index, tMap := range testMaps {
			var config Configuration
			err := config.FromMap(tMap)
			Expect(err).ToNot(HaveOccurred())
			Expect(config).To(Equal(expectedConfigs[index]))
		}
	})

	It("errors when it fails to convert maps to Configuration objects", func() {
		testMaps := []map[string]string{
			{
				"allowReinstalls":         "foobar",
				"maxConcurrentReconciles": "5",
			},
			{
				"allowReinstalls":         "true",
				"maxConcurrentReconciles": "ONE",
			},
		}

		for _, tMap := range testMaps {
			var config Configuration
			err := config.FromMap(tMap)
			Expect(err).To(HaveOccurred())
		}
	})

	It("errors when unknown fields are found while converting maps to Configuration objects", func() {
		testMaps := []map[string]string{
			{
				"allowReinstall$":         "true",
				"maxConcurrentReconciles": "1",
			},
			{
				"allowReinstalls":         "false",
				"maxConcurrentReconciles": "10",
				"foo":                     "bar",
			},
		}

		for _, tMap := range testMaps {
			var config Configuration
			err := config.FromMap(tMap)
			Expect(err).To(HaveOccurred())
		}
	})
})
