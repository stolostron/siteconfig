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

package v1alpha1

import (
	"reflect"
	"testing"
)

func TestClusterInstanceSpecExtraAnnotationSearch(t *testing.T) {
	tests := []struct {
		name             string
		extraAnnotations map[string]map[string]string
		kind             string
		want             map[string]string
		ok               bool
	}{
		{
			name: "extra annotations for resource defined",
			extraAnnotations: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "TestKind",
			want: map[string]string{
				"foo": "bar",
			},
			ok: true,
		},
		{
			name: "extra annotations for resource not defined",
			extraAnnotations: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "NotDefined",
			want: nil,
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterInstanceSpec{
				ExtraAnnotations: tt.extraAnnotations,
			}
			got, ok := c.ExtraAnnotationSearch(tt.kind)
			if ok != tt.ok {
				t.Errorf("ClusterInstanceSpec.ExtraAnnotationSearch() ok = %v, want %v", ok, tt.ok)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ClusterInstanceSpec.ExtraAnnotationSearch() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeSpecExtraAnnotationSearch(t *testing.T) {
	tests := []struct {
		name                     string
		specExtraAnnotations     map[string]map[string]string
		nodeSpecExtraAnnotations map[string]map[string]string
		kind                     string
		want                     map[string]string
		ok                       bool
	}{
		{
			name: "extra annotations for resource defined at cluster-level",
			specExtraAnnotations: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "TestKind",
			want: map[string]string{
				"foo": "bar",
			},
			ok: true,
		},
		{
			name: "extra annotations for resource defined at node-level",
			nodeSpecExtraAnnotations: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "TestKind",
			want: map[string]string{
				"foo": "bar",
			},
			ok: true,
		},
		{
			name: "extra annotations for resource not defined at either cluster or node-level",
			nodeSpecExtraAnnotations: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "NotDefined",
			want: nil,
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterInstanceSpec{
				ExtraAnnotations: tt.specExtraAnnotations,
				Nodes: []NodeSpec{
					{
						ExtraAnnotations: tt.nodeSpecExtraAnnotations,
					},
				},
			}
			got, ok := c.Nodes[0].ExtraAnnotationSearch(tt.kind, c)
			if ok != tt.ok {
				t.Errorf("NodeSpec.ExtraAnnotationSearch() ok = %v, want %v", ok, tt.ok)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NodeSpec.ExtraAnnotationSearch() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterInstanceSpecExtraLabelSearch(t *testing.T) {
	tests := []struct {
		name        string
		extraLabels map[string]map[string]string
		kind        string
		want        map[string]string
		ok          bool
	}{
		{
			name: "extra label for resource defined",
			extraLabels: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "TestKind",
			want: map[string]string{
				"foo": "bar",
			},
			ok: true,
		},
		{
			name: "extra label for resource not defined",
			extraLabels: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "NotDefined",
			want: nil,
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterInstanceSpec{
				ExtraLabels: tt.extraLabels,
			}
			got, ok := c.ExtraLabelSearch(tt.kind)
			if ok != tt.ok {
				t.Errorf("ClusterInstanceSpec.ExtraLabelSearch() ok = %v, want %v", ok, tt.ok)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ClusterInstanceSpec.ExtraLabelSearch() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeSpecExtraLabelSearch(t *testing.T) {
	tests := []struct {
		name                string
		specExtraLabels     map[string]map[string]string
		nodeSpecExtraLabels map[string]map[string]string
		kind                string
		want                map[string]string
		ok                  bool
	}{
		{
			name: "extra labels for resource defined at cluster-level",
			specExtraLabels: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "TestKind",
			want: map[string]string{
				"foo": "bar",
			},
			ok: true,
		},
		{
			name: "extra labels for resource defined at node-level",
			nodeSpecExtraLabels: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "TestKind",
			want: map[string]string{
				"foo": "bar",
			},
			ok: true,
		},
		{
			name: "extra labels for resource not defined at either cluster or node-level",
			nodeSpecExtraLabels: map[string]map[string]string{
				"TestKind": {
					"foo": "bar",
				},
			},
			kind: "NotDefined",
			want: nil,
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterInstanceSpec{
				ExtraLabels: tt.specExtraLabels,
				Nodes: []NodeSpec{
					{
						ExtraLabels: tt.nodeSpecExtraLabels,
					},
				},
			}
			got, ok := c.Nodes[0].ExtraLabelSearch(tt.kind, c)
			if ok != tt.ok {
				t.Errorf("NodeSpec.ExtraLabelSearch() ok = %v, want %v", ok, tt.ok)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NodeSpec.ExtraLabelSearch() got = %v, want %v", got, tt.want)
			}
		})
	}
}
