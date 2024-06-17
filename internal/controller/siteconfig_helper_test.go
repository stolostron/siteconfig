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

package controller

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/sakhoury/siteconfig/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			name:                  "invalid JSON set in installConfigOverride at SiteConfig",
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
			siteConfig := &v1alpha1.SiteConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.SiteConfigSpec{
					NetworkType:            tc.networkType,
					InstallConfigOverrides: tc.installConfigOverride,
					CPUPartitioning:        tc.CPUPartitioning,
				},
			}
			actual, err := getInstallConfigOverrides(siteConfig)
			if err != nil {
				assert.Equal(t, tc.error, err, "The expected and actual value should be the same.")
			}
			assert.Equal(t, tc.expected, actual, "The expected and actual value should be the same.")

		})
	}

}

func Test_buildSiteData(t *testing.T) {
	NetworkType := "OVNKubernetes"
	InstallConfigOverrides := "{\"controlPlane\":{\"hyperthreading\":\"Disabled\"}}"
	CPUPartitioning := v1alpha1.CPUPartitioningNone
	expectedInstallConfigOverrides := "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"controlPlane\":{\"hyperthreading\":\"Disabled\"}}"

	testcases := []struct {
		siteConfig *v1alpha1.SiteConfig
		node       *v1alpha1.NodeSpec
		expected   SiteData
		error      error
		name       string
	}{
		{
			siteConfig: &v1alpha1.SiteConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.SiteConfigSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
					},
				},
			},
			node: nil,
			expected: SiteData{
				Site: v1alpha1.SiteConfigSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
					Nodes: []v1alpha1.NodeSpec{
						{
							HostName: "node1",
							Role:     "master",
						},
					},
				},
				SpecialVars: SpecialVars{
					CurrentNode:            v1alpha1.NodeSpec{},
					InstallConfigOverrides: expectedInstallConfigOverrides,
					ControlPlaneAgents:     1,
					WorkerAgents:           0,
				},
			},
			error: nil,
			name:  "single master-node SiteConfig with nodeId undefined",
		},

		{
			siteConfig: &v1alpha1.SiteConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: v1alpha1.SiteConfigSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
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
			node: &v1alpha1.NodeSpec{
				HostName: "node1",
				Role:     "master",
			},
			expected: SiteData{
				Site: v1alpha1.SiteConfigSpec{
					NetworkType:            NetworkType,
					InstallConfigOverrides: InstallConfigOverrides,
					CPUPartitioning:        CPUPartitioning,
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
				},
			},
			error: nil,
			name:  "3 node (2 master, 1 worker) SiteConfig with nodeId set to first node",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {

			actual, err := buildSiteData(tc.siteConfig, tc.node)
			if err != nil {
				assert.Equal(t, tc.error, err, "The expected and actual value should be the same.")
			}
			assert.Equal(t, tc.expected, *actual, "The expected and actual value should be the same.")

		})
	}

}

func Test_suppressManifest(t *testing.T) {
	type args struct {
		kind                string
		suppressedManifests []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "manifest found in suppressedManfiests",
			args: args{
				kind:                "BareMetalHost",
				suppressedManifests: []string{"foobar-1", "BareMetalHost", "foobar-2"},
			},
			want: true,
		},

		{
			name: "manifest does not exist in suppressedManfiests",
			args: args{
				kind:                "BareMetalHost",
				suppressedManifests: []string{"foobar-1", "foobar-2", "foobar-3"},
			},
			want: false,
		},

		{
			name: "no manifest specified",
			args: args{
				kind:                "",
				suppressedManifests: []string{"foobar-1", "foobar-2", "foobar-3"},
			},
			want: false,
		},

		{
			name: "suppressedManifests list is empty",
			args: args{
				kind:                "foobar-1",
				suppressedManifests: []string{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suppressManifest(tt.args.kind, tt.args.suppressedManifests); got != tt.want {
				t.Errorf("suppressManifest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_appendManifestAnnotations(t *testing.T) {
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

func Test_anyFieldDefined(t *testing.T) {
	type args struct {
		v interface{}
	}

	type testStruct struct {
		Name   string
		Number int
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "empty struct",
			args: args{
				v: struct{}{},
			},
			want: false,
		},

		{
			name: "pointer to empty struct",
			args: args{
				v: &struct{}{},
			},
			want: false,
		},

		{
			name: "non-empty struct with no fields defined",
			args: args{
				v: testStruct{},
			},
			want: false,
		},

		{
			name: "non-empty struct with fully defined fields",
			args: args{
				v: testStruct{
					Name:   "barfoo",
					Number: 200,
				},
			},
			want: true,
		},

		{
			name: "non-empty struct with partially defined fields",
			args: args{
				v: testStruct{
					Number: 200,
				},
			},
			want: true,
		},

		{
			name: "pointer to non-empty struct with partially defined fields",
			args: args{
				v: &testStruct{
					Number: 200,
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anyFieldDefined(tt.args.v); got != tt.want {
				t.Errorf("anyFieldDefined() = %v, want %v", got, tt.want)
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
