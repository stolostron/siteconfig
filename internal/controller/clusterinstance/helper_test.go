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

package clusterinstance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_getInstallConfigOverrides(t *testing.T) {

	testcases := []struct {
		networkType, installConfigOverride string
		CPUPartitioning                    v1alpha1.CPUPartitioningMode
		expected                           string
		error                              error
		name                               string
	}{
		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "{\"controlPlane\":{\"hyperthreading\":\"Disabled\"}}",
			CPUPartitioning:       v1alpha1.CPUPartitioningNone,
			expected:              "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"controlPlane\":{\"hyperthreading\":\"Disabled\"}}",
			error:                 nil,
			name:                  "single json object set at installConfigOverride",
		},

		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "{feature:{test:abc}}",
			CPUPartitioning:       v1alpha1.CPUPartitioningNone,
			expected:              "",
			error:                 fmt.Errorf("invalid json parameter set at installConfigOverride"),
			name:                  "invalid JSON set in installConfigOverride at ClusterInstance",
		},

		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "{\"controlPlane\":{\"hyperthreading\":\"Disabled\"},\"fips\":\"true\"}",
			CPUPartitioning:       v1alpha1.CPUPartitioningNone,
			expected:              "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"controlPlane\":{\"hyperthreading\":\"Disabled\"},\"fips\":\"true\"}",
			error:                 nil,
			name:                  "multiple json object set at installConfigOverride",
		},

		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "",
			CPUPartitioning:       v1alpha1.CPUPartitioningNone,
			expected:              "{\"networking\":{\"networkType\":\"OVNKubernetes\"}}",
			error:                 nil,
			name:                  "json object when installConfigOverride is not set",
		},

		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "{\"networking\":{\"UserManagedNetworking\":\"True\",\"DeprecatedType\":\"test\"},\"features\":[{\"abc\":\"test\"},{\"xyz\":\"test1\"}]}",
			CPUPartitioning:       v1alpha1.CPUPartitioningNone,
			expected:              "{\"features\":[{\"abc\":\"test\"},{\"xyz\":\"test1\"}],\"networking\":{\"DeprecatedType\":\"test\",\"UserManagedNetworking\":\"True\",\"networkType\":\"OVNKubernetes\"}}",
			error:                 nil,
			name:                  "installConfigOverride contains non-overlapping networking settings",
		},

		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "{\"networking\":{\"UserManagedNetworking\":\"True\",\"networkType\":\"default\"},\"features\":[{\"abc\":\"test\"},{\"xyz\":\"test1\"}]}",
			CPUPartitioning:       v1alpha1.CPUPartitioningNone,
			expected:              "{\"features\":[{\"abc\":\"test\"},{\"xyz\":\"test1\"}],\"networking\":{\"UserManagedNetworking\":\"True\",\"networkType\":\"OVNKubernetes\"}}",
			error:                 nil,
			name:                  "installConfigOverride contains bad networking settings",
		},

		{
			networkType:           "OVNKubernetes",
			installConfigOverride: "{\"controlPlane\":{\"hyperthreading\":\"Disabled\"},\"fips\":\"true\"}",
			CPUPartitioning:       v1alpha1.CPUPartitioningAllNodes,
			expected:              "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"controlPlane\":{\"hyperthreading\":\"Disabled\"},\"cpuPartitioningMode\":\"AllNodes\",\"fips\":\"true\"}",
			error:                 nil,
			name:                  "cpuPartitioningMode set to AllNodes",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					NetworkType:            tc.networkType,
					InstallConfigOverrides: tc.installConfigOverride,
					CPUPartitioning:        tc.CPUPartitioning,
				},
			}
			actual, err := getInstallConfigOverrides(clusterInstance)
			if err != nil {
				assert.Equal(t, tc.error, err, "The expected and actual value should be the same.")
			}
			assert.Equal(t, tc.expected, actual, "The expected and actual value should be the same.")

		})
	}

}

func Test_buildClusterData(t *testing.T) {
	NetworkType := "OVNKubernetes"
	InstallConfigOverrides := "{\"controlPlane\":{\"hyperthreading\":\"Disabled\"}}"
	CPUPartitioning := v1alpha1.CPUPartitioningNone
	expectedInstallConfigOverrides := "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"controlPlane\":{\"hyperthreading\":\"Disabled\"}}"

	testcases := []struct {
		clusterInstance *v1alpha1.ClusterInstance
		clusterImageSet *hivev1.ClusterImageSet
		node            *v1alpha1.NodeSpec
		expected        ClusterData
		error           error
		name            string
	}{
		{
			clusterInstance: &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					ClusterType:            v1alpha1.ClusterTypeSNO,
					ClusterImageSetNameRef: "test-clusterimageset",
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
						{
							HostName: "node2",
							Role:     "worker",
						},
					},
				},
			},
			clusterImageSet: &hivev1.ClusterImageSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-clusterimageset",
					Namespace: "",
				},
				Spec: hivev1.ClusterImageSetSpec{
					ReleaseImage: "test-image",
				},
			},
			node: nil,
			expected: ClusterData{
				Spec: v1alpha1.ClusterInstanceSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					ClusterType:            v1alpha1.ClusterTypeSNO,
					ClusterImageSetNameRef: "test-clusterimageset",
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
						{
							HostName: "node2",
							Role:     "worker",
						},
					},
				},
				SpecialVars: SpecialVars{
					CurrentNode:            v1alpha1.NodeSpec{},
					InstallConfigOverrides: expectedInstallConfigOverrides,
					ControlPlaneAgents:     1,
					WorkerAgents:           0,
					ReleaseImage:           "test-image",
				},
			},
			error: nil,
			name:  "single master-node ClusterInstance with nodeId undefined",
		},

		{
			clusterInstance: &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					ClusterImageSetNameRef: "test-clusterimageset",
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
						{
							HostName: "node2",
							Role:     "master",
						},
						{
							HostName: "node3",
							Role:     "worker",
						},
					},
				},
			},
			clusterImageSet: &hivev1.ClusterImageSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-clusterimageset",
					Namespace: "",
				},
				Spec: hivev1.ClusterImageSetSpec{
					ReleaseImage: "test-image",
				},
			},
			node: &v1alpha1.NodeSpec{
				HostName: "node1",
				Role:     "master",
			},
			expected: ClusterData{
				Spec: v1alpha1.ClusterInstanceSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					ClusterImageSetNameRef: "test-clusterimageset",
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
						{
							HostName: "node2",
							Role:     "master",
						},
						{
							HostName: "node3",
							Role:     "worker",
						},
					},
				},
				SpecialVars: SpecialVars{
					CurrentNode: v1alpha1.NodeSpec{
						HostName: "node1",
						Role:     "master",
					},
					InstallConfigOverrides: expectedInstallConfigOverrides,
					ControlPlaneAgents:     2,
					WorkerAgents:           1,
					ReleaseImage:           "test-image",
				},
			},
			error: nil,
			name:  "3 node (2 master, 1 worker) ClusterInstance with nodeId set to first node",
		},
		{
			clusterInstance: &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					ClusterType:            v1alpha1.ClusterTypeSNO,
					ClusterImageSetNameRef: "foobar",
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
						{
							HostName: "node2",
							Role:     "worker",
						},
					},
				},
			},
			clusterImageSet: &hivev1.ClusterImageSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-clusterimageset",
					Namespace: "",
				},
				Spec: hivev1.ClusterImageSetSpec{
					ReleaseImage: "test-image",
				},
			},
			node:     nil,
			expected: ClusterData{},
			error:    fmt.Errorf("failed to get ClusterImageSet foobar: clusterimagesets.hive.openshift.io \"foobar\" not found"),
			name:     "ClusterInstance with invalid ClusterImageSetNameRef",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			// Create fake client with ClusterImageSet
			testScheme := scheme.Scheme
			schemeErr := hivev1.AddToScheme(testScheme)
			assert.NoError(t, schemeErr)
			c := fakeclient.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tc.clusterImageSet).
				Build()
			ctx := context.Background()

			actual, err := buildClusterData(ctx, c, tc.clusterInstance, tc.node)
			if tc.error != nil {
				assert.Error(t, err, "Expected an error but got none")
				assert.Equal(t, tc.error.Error(), err.Error(), "The expected and actual error message should be the same.")
			} else {
				assert.NoError(t, err, "Expected no error but got one")
				assert.NotNil(t, actual, "Expected data but got nil")
				assert.Equal(t, tc.expected, *actual, "The expected and actual value should be the same.")
			}

		})
	}

}

func TestPruneManifest(t *testing.T) {
	type args struct {
		resource  v1alpha1.ResourceRef
		pruneList []v1alpha1.ResourceRef
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "manifest found in pruneList",
			args: args{
				resource: v1alpha1.ResourceRef{APIVersion: "metal3.io/v1alpha1", Kind: "BareMetalHost"},
				pruneList: []v1alpha1.ResourceRef{
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-1"},
					{APIVersion: "metal3.io/v1alpha1", Kind: "BareMetalHost"},
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-2"},
				},
			},
			want: true,
		},

		{
			name: "manifest does not exist in pruneList",
			args: args{
				resource: v1alpha1.ResourceRef{APIVersion: "metal3.io/v1alpha1", Kind: "BareMetalHost"},
				pruneList: []v1alpha1.ResourceRef{
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-1"},
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-2"},
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-3"},
				},
			},
			want: false,
		},

		{
			name: "missing apiVersion",
			args: args{
				resource: v1alpha1.ResourceRef{Kind: "BareMetalHost"},
				pruneList: []v1alpha1.ResourceRef{
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-1"},
					{APIVersion: "metal3.io/v1alpha1", Kind: "BareMetalHost"},
					{APIVersion: "metal3.io/v1alpha1", Kind: "foobar-2"},
				},
			},
			want: false,
		},

		{
			name: "pruneList is empty",
			args: args{
				resource:  v1alpha1.ResourceRef{APIVersion: "metal3.io/v1alpha1", Kind: "BareMetalHost"},
				pruneList: []v1alpha1.ResourceRef{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pruneManifest(tt.args.resource, tt.args.pruneList); got != tt.want {
				t.Errorf("pruneManifest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSuppressManifest(t *testing.T) {
	type args struct {
		kind                  string
		suppressManifestsList []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "manifest found in suppressManifestsList",
			args: args{
				kind:                  "BareMetalHost",
				suppressManifestsList: []string{"foobar-1", "BareMetalHost", "foobar-2"},
			},
			want: true,
		},

		{
			name: "manifest does not exist in suppressManifestsList",
			args: args{
				kind:                  "BareMetalHost",
				suppressManifestsList: []string{"foobar-1", "foobar-2", "foobar-3"},
			},
			want: false,
		},

		{
			name: "no manifest specified",
			args: args{
				kind:                  "",
				suppressManifestsList: []string{"foobar-1", "foobar-2", "foobar-3"},
			},
			want: false,
		},

		{
			name: "suppressManifestsList is empty",
			args: args{
				kind:                  "foobar-1",
				suppressManifestsList: []string{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suppressManifest(tt.args.kind, tt.args.suppressManifestsList); got != tt.want {
				t.Errorf("suppressManifest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppendToManifestMetadata(t *testing.T) {
	type args struct {
		appendData map[string]string
		field      string
		manifest   map[string]interface{}
	}
	tests := []struct {
		name string
		args args
		want map[string]interface{}
	}{
		{
			name: "add new metadata field",
			args: args{
				appendData: map[string]string{
					"foo": "bar",
				},
				field: "newField",
				manifest: map[string]interface{}{
					"metadata": map[string]interface{}{
						"newField": map[string]interface{}{
							"test": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"newField": map[string]interface{}{
						"test": "ok",
						"foo":  "bar",
					},
				},
			},
		},

		{
			name: "should not modify existing field",
			args: args{
				appendData: map[string]string{
					"test": "foobar",
				},
				field: "testField",
				manifest: map[string]interface{}{
					"metadata": map[string]interface{}{
						"testField": map[string]interface{}{
							"test": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"testField": map[string]interface{}{
						"test": "ok",
					},
				},
			},
		},

		{
			name: "edge-case: create missing metadata map in addition to new field",
			args: args{
				appendData: map[string]string{
					"foo": "bar",
				},
				field: "newField",
				manifest: map[string]interface{}{
					"spec": map[string]interface{}{
						"field1": map[string]interface{}{
							"subField1": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"newField": map[string]interface{}{
						"foo": "bar",
					},
				},
				"spec": map[string]interface{}{
					"field1": map[string]interface{}{
						"subField1": "ok",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendToManifestMetadata(tt.args.appendData, tt.args.field, tt.args.manifest); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("appendToManifestMetadata() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppendManifestAnnotations(t *testing.T) {
	type args struct {
		extraAnnotations map[string]string
		manifest         map[string]interface{}
	}
	tests := []struct {
		name string
		args args
		want map[string]interface{}
	}{
		{
			name: "add new annotation",
			args: args{
				extraAnnotations: map[string]string{
					"foo": "bar",
				},
				manifest: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							"test": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"test": "ok",
						"foo":  "bar",
					},
				},
			},
		},

		{
			name: "should not modify existing annotation",
			args: args{
				extraAnnotations: map[string]string{
					"test": "foobar",
				},
				manifest: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							"test": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"test": "ok",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendManifestAnnotations(tt.args.extraAnnotations, tt.args.manifest); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("appendManifestAnnotations() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppendManifestLabels(t *testing.T) {
	type args struct {
		extraLabels map[string]string
		manifest    map[string]interface{}
	}
	tests := []struct {
		name string
		args args
		want map[string]interface{}
	}{
		{
			name: "add new label",
			args: args{
				extraLabels: map[string]string{
					"foo": "bar",
				},
				manifest: map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"test": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"test": "ok",
						"foo":  "bar",
					},
				},
			},
		},

		{
			name: "should not modify existing label",
			args: args{
				extraLabels: map[string]string{
					"test": "foobar",
				},
				manifest: map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"test": "ok",
						},
					},
				},
			},
			want: map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"test": "ok",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendManifestLabels(tt.args.extraLabels, tt.args.manifest); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("appendManifestLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_mergeJSONCommonKey(t *testing.T) {
	type args struct {
		mergeWith string
		mergeTo   string
		key       string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "Valid JSON strings and common key",
			args: args{
				mergeWith: `{"key":{"a":1,"b":2}}`,
				mergeTo:   `{"key":{"c":3,"d":4}}`,
				key:       "key",
			},
			want:    `{"key":{"a":1,"b":2,"c":3,"d":4}}`,
			wantErr: false,
		},

		{
			name: "Invalid JSON strings",
			args: args{
				mergeWith: `{"key":{"a":1}`,
				mergeTo:   `{"key":{"b":2}}`,
				key:       "key",
			},
			want:    "",
			wantErr: true,
		},

		{
			name: "Non-existent key in mergeWith",
			args: args{
				mergeWith: `{"foobar":{"a":1,"b":2}}`,
				mergeTo:   `{"key":{"c":3,"d":4}}`,
				key:       "key",
			},
			want:    "",
			wantErr: true,
		},

		{
			name: "Non-existent key in mergeTo",
			args: args{
				mergeWith: `{"key":{"a":1,"b":2}}`,
				mergeTo:   `{"foobar":{"c":3,"d":4}}`,
				key:       "key",
			},
			want:    "",
			wantErr: true,
		},

		{
			name: "Values associated with key are not maps",
			args: args{
				mergeWith: `{"key":1`,
				mergeTo:   `{"key":2`,
				key:       "key",
			},
			want:    ``,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeJSONCommonKey(tt.args.mergeWith, tt.args.mergeTo, tt.args.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("mergeJSONCommonKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("mergeJSONCommonKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

var _ = Describe("GetDeletedKeys", func() {
	It("should return empty slice when no deletions", func() {
		oldMap := map[string]string{"key1": "value1", "key2": "value2"}
		newMap := map[string]string{"key1": "value1", "key2": "value2"}
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(BeEmpty())
	})

	It("should detect one deletion", func() {
		oldMap := map[string]string{"key1": "value1", "key2": "value2"}
		newMap := map[string]string{"key1": "value1"}
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(ConsistOf("key2"))
	})

	It("should detect multiple deletions", func() {
		oldMap := map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"}
		newMap := map[string]string{"key1": "value1"}
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(ConsistOf("key2", "key3"))
	})

	It("should detect all deletions", func() {
		oldMap := map[string]string{"key1": "value1", "key2": "value2"}
		newMap := map[string]string{}
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(ConsistOf("key1", "key2"))
	})

	It("should return empty slice when only additions occur", func() {
		oldMap := map[string]string{"key1": "value1"}
		newMap := map[string]string{"key1": "value1", "key2": "value2"}
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(BeEmpty())
	})

	It("should handle nil old map", func() {
		var oldMap map[string]string
		newMap := map[string]string{"key1": "value1"}
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(BeEmpty())
	})

	It("should handle nil new map", func() {
		oldMap := map[string]string{"key1": "value1", "key2": "value2"}
		var newMap map[string]string
		result := GetDeletedKeys(oldMap, newMap)
		Expect(result).To(ConsistOf("key1", "key2"))
	})
})

var _ = Describe("GetLastAppliedExtraAnnotations", func() {
	var (
		lastAppliedSpec *v1alpha1.ClusterInstanceSpec
		lastAppliedNode *v1alpha1.NodeSpec
	)

	BeforeEach(func() {
		lastAppliedSpec = &v1alpha1.ClusterInstanceSpec{
			ExtraAnnotations: map[string]map[string]string{
				"ManagedCluster": {
					"cluster-anno": "cluster-value",
				},
			},
		}

		lastAppliedNode = &v1alpha1.NodeSpec{
			ExtraAnnotations: map[string]map[string]string{
				"BareMetalHost": {
					"node-anno": "node-value",
				},
			},
		}
	})

	It("should return cluster-level annotations", func() {
		result := GetLastAppliedExtraAnnotations(lastAppliedSpec, nil, "ManagedCluster")
		expected := map[string]string{"cluster-anno": "cluster-value"}
		Expect(result).To(Equal(expected))
	})

	It("should return node-level annotations", func() {
		result := GetLastAppliedExtraAnnotations(lastAppliedSpec, lastAppliedNode, "BareMetalHost")
		expected := map[string]string{"node-anno": "node-value"}
		Expect(result).To(Equal(expected))
	})

	It("should fallback to cluster-level when node doesn't have the kind", func() {
		result := GetLastAppliedExtraAnnotations(lastAppliedSpec, lastAppliedNode, "ManagedCluster")
		expected := map[string]string{"cluster-anno": "cluster-value"}
		Expect(result).To(Equal(expected))
	})

	It("should return nil when spec is nil", func() {
		result := GetLastAppliedExtraAnnotations(nil, nil, "ManagedCluster")
		Expect(result).To(BeNil())
	})

	It("should return nil when no annotations exist for kind", func() {
		result := GetLastAppliedExtraAnnotations(lastAppliedSpec, nil, "NonExistentKind")
		Expect(result).To(BeNil())
	})
})

var _ = Describe("GetLastAppliedExtraLabels", func() {
	var (
		lastAppliedSpec *v1alpha1.ClusterInstanceSpec
		lastAppliedNode *v1alpha1.NodeSpec
	)

	BeforeEach(func() {
		lastAppliedSpec = &v1alpha1.ClusterInstanceSpec{
			ExtraLabels: map[string]map[string]string{
				"ManagedCluster": {
					"cluster-label": "cluster-value",
				},
			},
		}

		lastAppliedNode = &v1alpha1.NodeSpec{
			ExtraLabels: map[string]map[string]string{
				"BareMetalHost": {
					"node-label": "node-value",
				},
			},
		}
	})

	It("should return cluster-level labels", func() {
		result := GetLastAppliedExtraLabels(lastAppliedSpec, nil, "ManagedCluster")
		expected := map[string]string{"cluster-label": "cluster-value"}
		Expect(result).To(Equal(expected))
	})

	It("should return node-level labels", func() {
		result := GetLastAppliedExtraLabels(lastAppliedSpec, lastAppliedNode, "BareMetalHost")
		expected := map[string]string{"node-label": "node-value"}
		Expect(result).To(Equal(expected))
	})

	It("should fallback to cluster-level when node doesn't have the kind", func() {
		result := GetLastAppliedExtraLabels(lastAppliedSpec, lastAppliedNode, "ManagedCluster")
		expected := map[string]string{"cluster-label": "cluster-value"}
		Expect(result).To(Equal(expected))
	})

	It("should return nil when spec is nil", func() {
		result := GetLastAppliedExtraLabels(nil, nil, "ManagedCluster")
		Expect(result).To(BeNil())
	})

	It("should return nil when no labels exist for kind", func() {
		result := GetLastAppliedExtraLabels(lastAppliedSpec, nil, "NonExistentKind")
		Expect(result).To(BeNil())
	})
})

var _ = Describe("GetLastAppliedSpec", func() {
	var clusterInstance *v1alpha1.ClusterInstance

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-cluster",
				Namespace:   "test-namespace",
				Annotations: make(map[string]string),
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "test-cluster",
			},
		}
	})

	It("should extract valid last applied spec from annotation", func() {
		lastAppliedSpec := &v1alpha1.ClusterInstanceSpec{
			ClusterName: "old-cluster-name",
			ExtraLabels: map[string]map[string]string{
				"ManagedCluster": {
					"test-label": "test-value",
				},
			},
		}

		lastSpecJSON, err := json.Marshal(lastAppliedSpec)
		Expect(err).ToNot(HaveOccurred())
		clusterInstance.Annotations[v1alpha1.LastClusterInstanceSpecAnnotation] = string(lastSpecJSON)

		result, err := GetLastAppliedSpec(clusterInstance)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.ClusterName).To(Equal("old-cluster-name"))
		Expect(result.ExtraLabels).To(HaveKey("ManagedCluster"))
		Expect(result.ExtraLabels["ManagedCluster"]).To(HaveKeyWithValue("test-label", "test-value"))
	})

	It("should return error when annotation is missing", func() {
		result, err := GetLastAppliedSpec(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("last applied spec annotation not found"))
		Expect(result).To(BeNil())
	})

	It("should return error when annotation contains invalid JSON", func() {
		clusterInstance.Annotations[v1alpha1.LastClusterInstanceSpecAnnotation] = "invalid-json"

		result, err := GetLastAppliedSpec(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to unmarshal last applied spec"))
		Expect(result).To(BeNil())
	})

	It("should return error when annotation is empty", func() {
		clusterInstance.Annotations[v1alpha1.LastClusterInstanceSpecAnnotation] = ""

		result, err := GetLastAppliedSpec(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to unmarshal last applied spec"))
		Expect(result).To(BeNil())
	})

	It("should handle complex spec with all fields", func() {
		lastAppliedSpec := &v1alpha1.ClusterInstanceSpec{
			ClusterName: "complex-cluster",
			ExtraLabels: map[string]map[string]string{
				"ManagedCluster": {
					"label1": "value1",
					"label2": "value2",
				},
				"BareMetalHost": {
					"bmh-label": "bmh-value",
				},
			},
			ExtraAnnotations: map[string]map[string]string{
				"ManagedCluster": {
					"anno1": "value1",
				},
			},
		}

		lastSpecJSON, err := json.Marshal(lastAppliedSpec)
		Expect(err).ToNot(HaveOccurred())
		clusterInstance.Annotations[v1alpha1.LastClusterInstanceSpecAnnotation] = string(lastSpecJSON)

		result, err := GetLastAppliedSpec(clusterInstance)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.ClusterName).To(Equal("complex-cluster"))
		Expect(result.ExtraLabels).To(HaveLen(2))
		Expect(result.ExtraAnnotations).To(HaveLen(1))
		Expect(result.ExtraLabels["ManagedCluster"]).To(HaveKeyWithValue("label1", "value1"))
		Expect(result.ExtraLabels["ManagedCluster"]).To(HaveKeyWithValue("label2", "value2"))
		Expect(result.ExtraLabels["BareMetalHost"]).To(HaveKeyWithValue("bmh-label", "bmh-value"))
		Expect(result.ExtraAnnotations["ManagedCluster"]).To(HaveKeyWithValue("anno1", "value1"))
	})
})
