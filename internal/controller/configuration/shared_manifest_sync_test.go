/*
Copyright 2025 Red Hat, Inc.

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
	"github.com/onsi/gomega"
	"testing"
)

func TestSharedManifestSyncConcurrency(t *testing.T) {
	for _, value := range []string{"1", "8", "0", "-1", "invalid", "1.5", "99999999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			g := gomega.NewWithT(t)
			config := NewDefaultConfiguration()
			store, err := NewConfigurationStore(NewDefaultConfiguration())
			g.Expect(err).NotTo(gomega.HaveOccurred())
			data := map[string]string{SharedManifestSyncMaxConcurrentReconcilesKey: value}
			parseErr := config.FromMap(data)
			updateErr := store.UpdateConfiguration(data)
			if value == "1" || value == "8" {
				g.Expect(parseErr).NotTo(gomega.HaveOccurred())
				g.Expect(updateErr).NotTo(gomega.HaveOccurred())
				g.Expect(config.ToMap()[SharedManifestSyncMaxConcurrentReconcilesKey]).To(gomega.Equal(value))
				g.Expect(store.GetSharedManifestSyncMaxConcurrentReconciles()).To(gomega.Equal(config.sharedManifestSyncMaxConcurrentReconciles))
			} else {
				g.Expect(parseErr).To(gomega.HaveOccurred())
				g.Expect(updateErr).To(gomega.HaveOccurred())
				g.Expect(store.GetSharedManifestSyncMaxConcurrentReconciles()).To(gomega.Equal(4))
				g.Expect(config.sharedManifestSyncMaxConcurrentReconciles).To(gomega.Equal(4))
			}
			g.Expect(store.GetMaxConcurrentReconciles()).To(gomega.Equal(1))
		})
	}
}

func TestExistingConfigurationRetainsSyncDefault(t *testing.T) {
	g := gomega.NewWithT(t)
	config := NewDefaultConfiguration()
	g.Expect(config.FromMap(map[string]string{"allowReinstalls": "true", "maxConcurrentReconciles": "7"})).To(gomega.Succeed())
	store, err := NewConfigurationStore(config)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(store.GetSharedManifestSyncMaxConcurrentReconciles()).To(gomega.Equal(4))
	g.Expect(store.GetMaxConcurrentReconciles()).To(gomega.Equal(7))
	g.Expect(store.UpdateConfiguration(map[string]string{SharedManifestSyncMaxConcurrentReconcilesKey: "8"})).To(gomega.Succeed())
	g.Expect(store.UpdateConfiguration(map[string]string{"allowReinstalls": "false"})).To(gomega.Succeed())
	g.Expect(store.GetSharedManifestSyncMaxConcurrentReconciles()).To(gomega.Equal(8))
}
