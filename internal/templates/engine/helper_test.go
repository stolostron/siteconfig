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

package engine

import (
	"reflect"
	"testing"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

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
